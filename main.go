package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	goversion "github.com/caarlos0/go-version"
	"github.com/go-git/go-git/v5"
	"gopkg.in/yaml.v3"
	"text/template"
)

/*
 * initialization and commands
 */

var logger *log.Logger

var (
	version   = "dev"
	treeState = ""
	commit    = ""
	date      = ""
	builtBy   = ""
)

var (
	flagVerbose bool
	flagDotFile string
)

var (
	syncCmd     = flag.NewFlagSet("sync", flag.ExitOnError)
	rmCmd       = flag.NewFlagSet("rm", flag.ExitOnError)
	diffCmd     = flag.NewFlagSet("diff", flag.ExitOnError)
	validateCmd = flag.NewFlagSet("validate", flag.ExitOnError)
)

func init() {
	logger = log.New(os.Stderr, "", 0)

	for _, fs := range []*flag.FlagSet{syncCmd, rmCmd, diffCmd, validateCmd} {
		fs.StringVar(&flagDotFile, "f", "dotkit.yml", "the dots config file")
	}
	for _, fs := range []*flag.FlagSet{syncCmd, rmCmd} {
		fs.BoolVar(&flagVerbose, "verbose", false, "verbose output")
	}
}

const usage = `Usage: dotkit <command> [flags]

Commands:
  sync       sync dotfiles to their destinations
  rm         remove mapped dotfiles
  diff       show dotfiles that are out of sync
  validate   validate the dots config file
  version    print version information
`

func printVersionInfo() {
	art := `
     |        _|_  |  _|_
   __|  __ _|_ |   |_  |
  /  | /  \_|  |__ |   |
o\_/|_/\__/ |_/    |   |_/
`
	logger.Print(goversion.GetVersionInfo(
		goversion.WithAppDetails("dotkit", "a minimal dotfiles manager", ""),
		goversion.WithASCIIName(art),
		func(i *goversion.Info) {
			i.GitCommit = commit
			i.GitTreeState = treeState
			i.BuildDate = date
			i.GitVersion = version
			i.BuiltBy = builtBy
		},
	))
}

/*
 * core data structures and operations
 */

type FileMapping struct {
	From string
	To   string
	As   string
	Os   string
	With map[string]string
}

func (m FileMapping) doLink() error {
	err := os.Symlink(m.From, m.To)
	if err != nil {
		return err
	}
	return nil
}

func (m FileMapping) doCopy() error {
	var inReader io.Reader
	if len(m.With) > 0 {
		in, err := os.ReadFile(m.From)
		if err != nil {
			panic(err)
		}
		inReader = strings.NewReader(evalTemplateString(string(in), m.With))
	} else {
		fin, err := os.Open(m.From)
		if err != nil {
			return err
		}
		defer fin.Close()
		inReader = fin
	}

	fout, err := os.Create(m.To)
	if err != nil {
		return err
	}
	defer fout.Close()

	_, err = io.Copy(fout, inReader)
	if err != nil {
		return err
	}

	return nil
}

func unmapPath(p string) {
	if pathExists := pathExists(p); !pathExists {
		if flagVerbose {
			logger.Printf("rm %s: skipping, file not there\n", p)
		}
	} else {
		err := os.Remove(p)
		if err != nil {
			logger.Printf("failed removing file %s, %v\n", p, err)
		}
		if flagVerbose {
			logger.Printf("rm %s: success\n", p)
		}
	}
}

func (m FileMapping) domap() {
	handleDoMapRes := func(m FileMapping, err error) {
		if err != nil {
			logger.Fatalf("failed %s %s -> %s: %v", m.As+"ing", m.From, m.To, err)
		}
		if flagVerbose {
			logger.Printf("%s %s -> %s\n", m.As+"ing", m.From, m.To)
		}
	}

	// ensure destination path exists
	if err := createPath(m.To); err != nil {
		logger.Fatalf("failed creating path %s, %v", m.To, err)
	}

	switch typ := m.As; typ {
	case "link":
		err := m.doLink()
		handleDoMapRes(m, err)
	case "copy":
		err := m.doCopy()
		handleDoMapRes(m, err)
	}
}

func (m FileMapping) isMatchingOs() bool {
	osMap := map[string]string{
		"linux":  "linux",
		"macos":  "darwin",
		"darwin": "darwin",
		"all":    runtime.GOOS,
		"":       runtime.GOOS,
	}
	return osMap[m.Os] == runtime.GOOS
}

type Opts struct {
	Cd string
}

type Dots struct {
	Opts         Opts          `yaml:"opt"`
	FileMappings []FileMapping `yaml:"map"`
	Resources    []Resource    `yaml:"fetch"`
}

type Resource struct {
	Url  string `yaml:"url"`
	To   string `yaml:"to"`
	As   string `yaml:"as"`
	Skip bool   `yaml:"skip"`
}

func (d *Dots) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var tmpDots struct {
		Opts      Opts                   `yaml:"opt"`
		Mappings  map[string]FileMapping `yaml:"map"`
		Resources []Resource             `yaml:"fetch"`
	}
	err := unmarshal(&tmpDots)
	if err != nil {
		return err
	}
	d.Opts = tmpDots.Opts
	for file, mapping := range tmpDots.Mappings {
		mapping.From = file
		d.FileMappings = append(d.FileMappings, mapping)
	}
	d.Resources = tmpDots.Resources
	return nil
}

func (dots Dots) validate() []error {
	var errs []error
	for _, mapping := range dots.FileMappings {
		if !pathExists(mapping.From) {
			errs = append(errs, fmt.Errorf("%s: path does not exist", mapping.From))
		} else if isDirectory(mapping.From) && mapping.As == "copy" {
			errs = append(errs, fmt.Errorf("%s: cannot use copy type with directory", mapping.From))
		}

		if mapping.As != "copy" && len(mapping.With) > 0 {
			errs = append(errs, fmt.Errorf("%s: templating is only supported in `copy` mode ]", mapping.From))
		}
	}
	for _, resource := range dots.Resources {
		if len(resource.To) == 0 {
			errs = append(errs, fmt.Errorf("%s: resource destination (`to`) cannot be empty", resource.Url))
		}
		if len(resource.As) == 0 {
			errs = append(errs, fmt.Errorf("%s: resource type (`as`) cannot be empty", resource.Url))
		}
	}
	return errs
}

func inferDestination(file string) string {
	if strings.HasPrefix(file, ".") {
		return getHomeDir() + "/" + file
	} else {
		return getHomeDir() + "/." + file
	}
}

func evalTemplateString(templStr string, env map[string]string) string {
	templ, err := template.New("template").Parse(templStr)
	if err != nil {
		logger.Fatalf("failed creating template from %s, %v", templStr, err)
	}
	var templOut bytes.Buffer
	err = templ.Execute(&templOut, env)
	if err != nil {
		logger.Fatalf("failed executing template, %v", err)
	}
	return templOut.String()
}

func evalTemplate(with map[string]string) map[string]string {
	newMap := make(map[string]string, len(with))
	for variable, templ := range with {
		env := map[string]string{"Os": runtime.GOOS}
		newMap[variable] = evalTemplateString(templ, env)
	}
	return newMap
}

func (dots Dots) transform() Dots {
	opts := dots.Opts
	mappings := dots.FileMappings

	var newDots Dots
	newDots.Opts = opts

	for _, mapping := range mappings {
		// To is expanded / inferred first: it's value is based off of
		// `from` before prefix or cwd are added to it
		if len(mapping.To) > 0 {
			// expand destination ~
			mapping.To = expandTilde(mapping.To)
		} else {
			// infer destination based on From
			mapping.To = inferDestination(mapping.From)
		}

		if len(mapping.With) > 0 {
			mapping.With = evalTemplate(mapping.With)
		}

		if len(opts.Cd) > 0 {
			// Cd set: add prefix to From
			mapping.From = path.Join(opts.Cd, mapping.From)
		}
		if !strings.HasPrefix(mapping.From, "/") {
			cwd, _ := os.Getwd()
			mapping.From = cwd + "/" + mapping.From
		}

		// default As to symlink
		if len(mapping.As) == 0 {
			mapping.As = "link"
		}

		newDots.FileMappings = append(newDots.FileMappings, mapping)
	}
	for _, resource := range dots.Resources {
		if len(resource.To) > 0 {
			resource.To = expandTilde(resource.To)
		}

		newDots.Resources = append(newDots.Resources, resource)
	}

	return newDots
}

func fetchGitResource(resource Resource) error {
	if err := createPath(resource.To); err != nil {
		return err
	}
	_, err := git.PlainClone(resource.To, false, &git.CloneOptions{
		URL:      resource.Url,
		Progress: os.Stdout,
	})

	return err
}

func fetchHttpResource(resource Resource) error {
	req, err := http.NewRequest("GET", resource.Url, nil)
	if err != nil {
		return err
	}
	httpClient := http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if !pathExists(resource.To) {
		if err := createPath(resource.To); err != nil {
			return err
		}
	}

	if strings.HasSuffix(resource.To, "/") {
		resource.To = filepath.Join(resource.To, path.Base(resource.Url))
	}

	fout, err := os.Create(resource.To)
	if err != nil {
		return err
	}
	defer fout.Close()

	if _, err := io.Copy(fout, resp.Body); err != nil {
		return err
	}

	return nil
}

func (r Resource) resolvedTo() string {
	if r.As == "file" && strings.HasSuffix(r.To, "/") {
		return filepath.Join(r.To, path.Base(r.Url))
	}
	return r.To
}

func fetchResource(resource Resource) error {
	switch resource.As {
	case "git":
		return fetchGitResource(resource)
	case "file":
		return fetchHttpResource(resource)
	}

	return nil
}

func (dots Dots) sync() {
	// Build a set of out-of-sync mappings so we can skip those already in sync
	diffEntries := dots.diff()
	outOfSync := make(map[string]DiffStatus, len(diffEntries))
	for _, e := range diffEntries {
		outOfSync[e.From] = e.Status
	}

	for _, mapping := range dots.FileMappings {
		if !mapping.isMatchingOs() {
			if flagVerbose {
				logger.Printf("not on %s, skipping %s\n", mapping.Os, mapping.From)
			}
			continue
		}
		status, needsSync := outOfSync[mapping.From]
		if !needsSync {
			if flagVerbose {
				logger.Printf("already in sync, skipping %s\n", mapping.From)
			}
			continue
		}
		// Skip copied items that already exist to avoid destroying user config
		if mapping.As == "copy" && status != DiffMissing {
			if flagVerbose {
				logger.Printf("copy target exists, skipping %s\n", mapping.From)
			}
			continue
		}
		unmapPath(mapping.To)
		mapping.domap()
	}
	for _, resource := range dots.Resources {
		if resource.Skip {
			continue
		}
		if pathExists(resource.resolvedTo()) {
			if flagVerbose {
				logger.Printf("already fetched, skipping %s\n", resource.Url)
			}
			continue
		}
		err := fetchResource(resource)
		if err != nil {
			logger.Printf("error fetching resource %s, %v", resource.Url, err)
		}
	}
}

func collapseTilde(p string) string {
	home := getHomeDir()
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

type DiffStatus int

const (
	DiffMissing  DiffStatus = iota // target does not exist
	DiffChanged                    // target exists but differs
	DiffError                      // could not compare
)

type DiffEntry struct {
	From    string
	To      string
	Status  DiffStatus
	Detail  string
}

func (dots Dots) diff() []DiffEntry {
	var entries []DiffEntry
	for _, mapping := range dots.FileMappings {
		if !mapping.isMatchingOs() {
			continue
		}
		switch mapping.As {
		case "link":
			target, err := os.Readlink(mapping.To)
			if err != nil {
				entries = append(entries, DiffEntry{mapping.From, mapping.To, DiffMissing, "not linked"})
			} else if target != mapping.From {
				entries = append(entries, DiffEntry{mapping.From, mapping.To, DiffChanged, "linked to " + target})
			}
		case "copy":
			if !pathExists(mapping.To) {
				entries = append(entries, DiffEntry{mapping.From, mapping.To, DiffMissing, "not copied"})
				continue
			}
			srcContent, err := mapping.expectedContent()
			if err != nil {
				entries = append(entries, DiffEntry{mapping.From, mapping.To, DiffError, err.Error()})
				continue
			}
			dstContent, err := os.ReadFile(mapping.To)
			if err != nil {
				entries = append(entries, DiffEntry{mapping.From, mapping.To, DiffError, err.Error()})
				continue
			}
			if !bytes.Equal(srcContent, dstContent) {
				entries = append(entries, DiffEntry{mapping.From, mapping.To, DiffChanged, "content differs"})
			}
		}
	}
	return entries
}

func printDiff(entries []DiffEntry) {
	if len(entries) == 0 {
		logger.Println("all dotfiles are in sync")
		return
	}
	for _, e := range entries {
		from := collapseTilde(e.From)
		to := collapseTilde(e.To)
		switch e.Status {
		case DiffMissing:
			logger.Printf("- %s -> %s (%s)\n", from, to, e.Detail)
		case DiffChanged:
			logger.Printf("~ %s -> %s (%s)\n", from, to, collapseTilde(e.Detail))
		case DiffError:
			logger.Printf("! %s -> %s (%s)\n", from, to, e.Detail)
		}
	}
}

func (m FileMapping) expectedContent() ([]byte, error) {
	src, err := os.ReadFile(m.From)
	if err != nil {
		return nil, err
	}
	if len(m.With) > 0 {
		return []byte(evalTemplateString(string(src), m.With)), nil
	}
	return src, nil
}

func (dots Dots) rm() {
	for _, mapping := range dots.FileMappings {
		if !mapping.isMatchingOs() {
			if flagVerbose {
				logger.Printf("not on %s, skipping %s\n", mapping.Os, mapping.From)
			}
			continue
		}
		unmapPath(mapping.To)
	}
	for _, resource := range dots.Resources {
		unmapPath(resource.resolvedTo())
	}
}

/*
 * Helpers
 */

func createPath(p string) error {
	dstDir := path.Dir(p)
	if !pathExists(dstDir) {
		err := os.MkdirAll(dstDir, 0750)
		if err != nil {
			return err
		}
	}
	return nil
}

func pathExists(path string) bool {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

func isDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		logger.Fatalf("failed reading path %s, %v", path, err)
	}
	return fileInfo.IsDir()
}

func getHomeDir() string {
	return os.Getenv("HOME")
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~") {
		homeDir := getHomeDir()
		path = strings.Replace(path, "~", homeDir, 1)
	}
	return path
}

func readDotFile(file string) Dots {
	rcFileData, err := os.ReadFile(file)
	if err != nil {
		logger.Fatalf("error reading config data: %v", err)
	}

	var dots Dots

	decoder := yaml.NewDecoder(bytes.NewReader(rcFileData))
	decoder.KnownFields(true)

	if err := decoder.Decode(&dots); err != nil {
		logger.Fatalf("cannot decode data: %v", err)
	}

	newDots := dots.transform()
	errs := newDots.validate()
	if len(errs) > 0 {
		for _, err := range errs {
			logger.Printf("failed validating dots file: %v", err)
		}
		os.Exit(1)
	}

	return newDots
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "sync":
		syncCmd.Parse(os.Args[2:])
		dots := readDotFile(flagDotFile)
		dots.sync()
	case "rm":
		rmCmd.Parse(os.Args[2:])
		dots := readDotFile(flagDotFile)
		dots.rm()
	case "diff":
		diffCmd.Parse(os.Args[2:])
		dots := readDotFile(flagDotFile)
		printDiff(dots.diff())
	case "validate":
		validateCmd.Parse(os.Args[2:])
		dots := readDotFile(flagDotFile)
		_ = dots
		logger.Printf("yay, dots file valid!")
	case "version":
		printVersionInfo()
	default:
		fmt.Print(usage)
		os.Exit(1)
	}
}
