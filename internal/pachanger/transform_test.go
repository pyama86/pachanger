package pachanger_test

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pyama86/pachanger/internal/pachanger"
	"github.com/stretchr/testify/assert"
)

func TestTransformTargetFile(t *testing.T) {
	workDir, err := os.Getwd()
	assert.NoError(t, err)
	workDir = filepath.Join(workDir, "testdata")

	inputPath := "example/target_input.go"
	expectedPath := "target_expected.go"
	outputPath := "target_output.go"
	defer os.Remove(outputPath)

	fs := token.NewFileSet()
	pkgs, err := pachanger.LoadPackages(fs, workDir, nil)
	assert.NoError(t, err)

	node, pkg, err := pachanger.FindPackageForFile(fs, pkgs, filepath.Join(workDir, inputPath))
	assert.NoError(t, err)

	transformer := pachanger.NewTransformer(fs, workDir, filepath.Join(workDir, inputPath), "example", "", "changed_example", "")
	err = transformer.TransformSymbolsInTargetFile(node, pkg.TypesInfo, filepath.Join(workDir, outputPath))
	assert.NoError(t, err)

	diff, err := compareFiles(filepath.Join(workDir, outputPath), filepath.Join(workDir, expectedPath))
	assert.NoError(t, err)
	assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))
}

func TestTransformOtherFile(t *testing.T) {
	workDir, err := os.Getwd()
	assert.NoError(t, err)
	workDir = filepath.Join(workDir, "testdata")

	fs := token.NewFileSet()
	pkgs, err := pachanger.LoadPackages(fs, workDir, nil)
	assert.NoError(t, err)
	targetPath := "example/target_input.go"
	targetPath = filepath.Join(workDir, targetPath)

	t.Run("same package", func(t *testing.T) {
		inputPath := "example/some_example.go"
		expectedPath := "some_example_expected.go"
		outputPath := "some_example_output.go"
		defer os.Remove(outputPath)

		node, pkg, err := pachanger.FindPackageForFile(fs, pkgs, filepath.Join(workDir, inputPath))
		assert.NoError(t, err)

		transformer := pachanger.NewTransformer(fs, workDir, targetPath, "example", "", "changed_example", "")
		err = transformer.TransformSymbolsInOtherFile(node, pkg.TypesInfo, filepath.Join(workDir, outputPath))
		assert.NoError(t, err)

		diff, err := compareFiles(filepath.Join(workDir, outputPath), filepath.Join(workDir, expectedPath))
		assert.NoError(t, err)
		assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))

	})

	t.Run("transform other file", func(t *testing.T) {
		inputPath := "someother/otherfile_input.go"
		expectedPath := "someother_otherfile_expected.go"
		outputPath := "someother_otherfile_output.go"
		defer os.Remove(outputPath)

		node, pkg, err := pachanger.FindPackageForFile(fs, pkgs, filepath.Join(workDir, inputPath))
		assert.NoError(t, err)

		transformer := pachanger.NewTransformer(fs, workDir, targetPath, "example", "", "changed_example", "")
		err = transformer.TransformSymbolsInOtherFile(node, pkg.TypesInfo, filepath.Join(workDir, outputPath))
		assert.NoError(t, err)

		diff, err := compareFiles(filepath.Join(workDir, outputPath), filepath.Join(workDir, expectedPath))
		assert.NoError(t, err)
		assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))
	})

	// パッケージ名が削除されるパターン
	t.Run("transform other file with delete package name", func(t *testing.T) {

		inputPath := "changed_example/otherfile_input.go"
		expectedPath := "changed_example_otherfile_expected.go"
		outputPath := "changed_example_otherfile_output.go"
		defer os.Remove(outputPath)

		node, pkg, err := pachanger.FindPackageForFile(fs, pkgs, filepath.Join(workDir, inputPath))
		assert.NoError(t, err)

		transformer := pachanger.NewTransformer(fs, workDir, targetPath, "example", "", "changed_example", "")
		err = transformer.TransformSymbolsInOtherFile(node, pkg.TypesInfo, filepath.Join(workDir, outputPath))
		assert.NoError(t, err)

		diff, err := compareFiles(filepath.Join(workDir, outputPath), filepath.Join(workDir, expectedPath))
		assert.NoError(t, err)
		assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))
	})
}

func compareFiles(fileA, fileB string) (string, error) {
	a, err := os.ReadFile(fileA)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(fileB)
	if err != nil {
		return "", err
	}
	if string(a) == string(b) {
		return "", nil
	}
	alines := strings.Split(string(a), "\n")
	blines := strings.Split(string(b), "\n")
	var diffs []string
	max := len(alines)
	if len(blines) > max {
		max = len(blines)
	}
	for i := 0; i < max; i++ {
		var x, y string
		if i < len(alines) {
			x = alines[i]
		}
		if i < len(blines) {
			y = blines[i]
		}
		if x != y {
			diffs = append(diffs, fmt.Sprintf("line %d:\n  got:  %s\n  want: %s", i+1, x, y))
		}
	}
	return strings.Join(diffs, "\n"), nil
}
