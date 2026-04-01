# dotkit 🏠📦🔗

A minimal, single-binary dotfiles manager written in Go.

Declaratively map your configuration files to their destinations using a simple 
YAML spec. Supports symlinks, file copies with templating, OS-specific rules, 
and fetching remote resources — all without dependencies.

## Install

### From releases

```sh
curl --remote-name-all --location $( \
    curl -s https://api.github.com/repos/gszr/dotkit/releases/latest \
    | grep "browser_download_url.*$(uname -s)-$(uname -m).*" \
    | cut -d : -f 2,3 \
    | tr -d \" )
```

Available for Linux and macOS on `x86_64` and `arm64`.

### From source

```sh
go install github.com/gszr/dotkit@latest
```

## Quick start

Create a `dotkit.yml` in your dotfiles directory:

```yaml
map:
  gitconfig:
  zshrc:
    os: macos
  config/alacritty.yml:
    to: ~/.config/alacritty/alacritty.yml

fetch:
- url: https://github.com/user/vim-plugin
  to: ~/.vim/pack/plugins/start/vim-plugin
  as: git
```

Then run:

```sh
dotkit sync
```

That's it. `gitconfig` gets symlinked to `~/.gitconfig`, `zshrc` to `~/.zshrc` (on macOS only), 
and the Vim plugin gets cloned.

## Commands

```
dotkit <command> [flags]

Commands:
  sync       sync dotfiles to their destinations
  rm         remove mapped dotfiles
  diff       show dotfiles that are out of sync
  validate   validate the dots config file
  version    print version and build information

Flags:
  -f string    config file (default "dotkit.yml")
  -verbose     verbose output (sync, rm)
```

### `dotkit sync`

Removes existing targets and re-creates all mappings. This is the primary command — run it whenever your dotfiles change.

### `dotkit diff`

Shows which dotfiles and fetched resources are out of sync without making changes:

```
- ~/dotfiles/zshrc -> ~/.zshrc (not linked)
~ ~/dotfiles/gitconfig -> ~/.gitconfig (linked to /wrong/path)
~ ~/dotfiles/gpg-agent.conf -> ~/.gnupg/gpg-agent.conf (content differs)
```

### `dotkit rm`

Removes all mapped targets. Useful for cleaning up before switching machines or dotfile repos.

### `dotkit validate`

Checks the config file for errors without applying anything.

## Configuration

### File mappings

Each entry under `map` is a file in the current directory (or under `opt.cd`):

```yaml
map:
  gitconfig:           # symlinks to ~/.gitconfig (inferred)
  zshrc:
    to: ~/.zshrc       # explicit destination
    as: copy           # copy instead of symlink
    os: linux          # only on Linux
```

| Field  | Description | Default |
|--------|-------------|---------|
| `to`   | Destination path. `~` is expanded. Parent directories are created automatically. | `~/.<filename>` |
| `as`   | `link` (symlink) or `copy` | `link` |
| `os`   | `linux`, `macos` (or `darwin`), or `all` | `all` |
| `with` | Template variables (copy mode only) | — |

### Templating

For files that need OS-specific content, use Go's [text/template](https://pkg.go.dev/text/template) syntax with `as: copy`:

```yaml
map:
  gnupg/gpg-agent.conf:
    as: copy
    with:
      PinentryPrefix: '{{if eq .Os "darwin"}}/opt/homebrew/bin{{else}}/usr/bin{{end}}'
```

The source file references the variable:

```
pinentry-program {{.PinentryPrefix}}/pinentry-tty
```

The `.Os` variable (matching Go's `runtime.GOOS`) is available in `with` values.

### Fetching remote resources

Clone Git repositories or download files as part of your setup:

```yaml
fetch:
- url: https://github.com/altercation/vim-colors-solarized
  to: ~/.vim/pack/plugins/start/vim-colors-solarized
  as: git
- url: https://example.com/script.sh
  to: ~/bin/
  as: file
```

| Field  | Description |
|--------|-------------|
| `url`  | Remote URL |
| `to`   | Local destination path. If it's a directory (for `file` mode), the filename is inferred from the URL. |
| `as`   | `git` (clone) or `file` (HTTP download) |
| `skip` | Set to `true` to skip fetching |

### Options

```yaml
opt:
  cd: dots/    # all source files are relative to this subdirectory
```

## Full example

```yaml
map:
  gitconfig:
  zshrc:
    os: macos
  i3:
    os: linux
  config/alacritty.yml:
    to: ~/.config/alacritty/alacritty.yml
  docker/config.json:
    as: copy
  gnupg/gpg-agent.conf:
    as: copy
    with:
      PinentryPrefix: '{{if eq .Os "darwin"}}/opt/homebrew/bin{{else}}/usr/bin{{end}}'

fetch:
- url: https://github.com/gszr/dynamic-colors
  to: ~/.dynamic-colors
  as: git

opt:
  cd: dots/
```

```
.
├── dots/
│   ├── config/
│   │   └── alacritty.yml
│   ├── docker/
│   │   └── config.json
│   ├── gnupg/
│   │   └── gpg-agent.conf
│   ├── gitconfig
│   ├── i3
│   └── zshrc
└── dotkit.yml
```

## License

[MIT](LICENSE)
