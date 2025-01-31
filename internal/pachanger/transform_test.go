package pachanger_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pyama86/pachanger/internal/pachanger"
	"github.com/stretchr/testify/assert"
)

func init() {
	level := slog.LevelInfo
	if os.Getenv("PACHANGER_DEBUG") != "" {
		level = slog.LevelDebug

	}

	slog.SetDefault(
		slog.New(
			slog.NewTextHandler(
				os.Stdout,
				&slog.HandlerOptions{Level: level},
			),
		),
	)
}

func TestTransformTargetFile(t *testing.T) {
	workDir, err := os.Getwd()
	assert.NoError(t, err)
	workDir = filepath.Join(workDir, "testdata")

	inputPath := filepath.Join(workDir, "example/target_input.go")
	expectedPath := filepath.Join(workDir, "target_expected.go")
	outputPath := filepath.Join(workDir, "target_output.go")
	os.Remove(outputPath)

	transformer, err := pachanger.NewTransformer(workDir, "changed_example", "", "", nil)
	assert.NoError(t, err)

	err = transformer.TransformSymbolsInTargetFile(inputPath, outputPath)
	assert.NoError(t, err)

	err = transformer.Dump()
	assert.NoError(t, err)

	diff, err := compareFiles(outputPath, expectedPath)
	assert.NoError(t, err)
	assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))
}

func TestTransformOtherFile(t *testing.T) {
	workDir, err := os.Getwd()
	assert.NoError(t, err)
	workDir = filepath.Join(workDir, "testdata")

	targetPath := filepath.Join(workDir, "example/target_input.go")
	targetOutputPath := filepath.Join(workDir, "target_output.go")

	// 同じパッケージの他のファイルが変換されるパターン
	t.Run("same package", func(t *testing.T) {
		inputPath := filepath.Join(workDir, "example/some_example.go")
		expectedPath := filepath.Join(workDir, "some_example_expected.go")
		outputPath := filepath.Join(workDir, "some_example_output.go")
		os.Remove(outputPath)

		transformer, err := pachanger.NewTransformer(workDir, "changed_example", "", "", nil)
		assert.NoError(t, err)

		err = transformer.TransformSymbolsInTargetFile(targetPath, targetOutputPath)
		assert.NoError(t, err)
		err = transformer.TransformSymbolsInOtherFile(inputPath, outputPath)
		assert.NoError(t, err)
		err = transformer.Dump()
		assert.NoError(t, err)

		diff, err := compareFiles(outputPath, expectedPath)
		assert.NoError(t, err)
		assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))

	})

	// example/input_target.goが変換された後に、someother/otherfile_input.goが変換されるパターン
	t.Run("transform other file", func(t *testing.T) {
		inputPath := filepath.Join(workDir, "someother/otherfile_input.go")
		expectedPath := filepath.Join(workDir, "someother_otherfile_expected.go")
		outputPath := filepath.Join(workDir, "someother_otherfile_output.go")
		os.Remove(outputPath)

		transformer, err := pachanger.NewTransformer(workDir, "changed_example", "", "", nil)
		assert.NoError(t, err)

		err = transformer.TransformSymbolsInTargetFile(targetPath, targetOutputPath)
		assert.NoError(t, err)
		err = transformer.TransformSymbolsInOtherFile(inputPath, outputPath)
		assert.NoError(t, err)
		err = transformer.Dump()
		assert.NoError(t, err)

		diff, err := compareFiles(outputPath, expectedPath)
		assert.NoError(t, err)
		assert.Empty(t, diff, fmt.Sprintf("Diff:\n%s", diff))
	})

	// パッケージ名が削除されるパターン
	t.Run("transform other file with delete package name", func(t *testing.T) {
		inputPath := filepath.Join(workDir, "changed_example/otherfile_input.go")
		expectedPath := filepath.Join(workDir, "changed_example_otherfile_expected.go")
		outputPath := filepath.Join(workDir, "changed_example_otherfile_output.go")
		os.Remove(outputPath)

		transformer, err := pachanger.NewTransformer(workDir, "changed_example", "", "", nil)
		assert.NoError(t, err)

		err = transformer.TransformSymbolsInTargetFile(targetPath, targetOutputPath)
		assert.NoError(t, err)
		err = transformer.TransformSymbolsInOtherFile(inputPath, outputPath)
		assert.NoError(t, err)
		err = transformer.Dump()
		assert.NoError(t, err)

		diff, err := compareFiles(outputPath, expectedPath)
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
