# pachanger

pachanger is a CLI tool that renames a Go package and updates all references in other files accordingly.

## Installation

You can install pachanger using Go:

### Go
```sh
% go install github.com/pyama86/pachanger@latest
```

### Homebrew
```sh
% brew install pyama86/homebrew-ptools/pachanger
```

Alternatively, you can clone the repository and build the binary manually:

```
% git clone https://github.com/pyama86/pachanger.git
% cd pachanger
% go build -o pachanger ./cmd
```

## Usage

### Basic Command

```sh
% pachanger --file <target-file> --new <new-package-name> [--output <output-directory>] [--workdir <working-directory>]
```

### Options

- `--file`    The target file where the package name should be changed (required).
- `--new`     The new package name (required).
- `--output`  Directory to save the modified file (default: same directory as input file).
- `--workdir` Working directory (default: current directory).

### Check Version

```
% pachanger version
```

## Examples

### Rename a package

Rename the package in `model/example.go` to `example` and save the modified file in `out/`:

```
% pachanger --file model/example.go --new example --output model/example
```

### Specify a working directory

Set `src` as the working directory and rename the package in `model/example.go` to `example`:

```
% pachanger --file model/example.go --new example --workdir src
```

## How It Works

1. The package name in the specified `--file` is changed to `--new`.
2. The modified file is saved in the `--output` directory.
3. The tool scans `.go` files in `--workdir` and updates references accordingly.
4. The code is formatted automatically using `goimports`.

## For Developers

### Build

```sh
% go build -o pachanger ./cmd
```

### Run Tests

```sh
% make test
```

## License

MIT License

## Author

[pyama86](https://github.com/pyama86)
