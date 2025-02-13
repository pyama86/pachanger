package pachanger_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pyama86/pachanger/internal/pachanger"
	"github.com/stretchr/testify/assert"
)

func TestMigrate(t *testing.T) {
	workDir, err := os.Getwd()
	assert.NoError(t, err)
	workDir = filepath.Join(workDir, "testdata")

	targetPkg := "migrate"
	suffix := "ForTestMigrate"

	structGoContent := `package migrate

type MigrateStruct struct {
	foo    string
	bar    int
	foobar *string
}
`
	if err := os.MkdirAll(filepath.Join(workDir, "migrate"), 0755); err != nil {
		assert.NoError(t, err)
	}

	structTestGoContent := `package migrate_test

import (
	"testing"

	"github.com/pyama86/pachanger/internal/pachanger/testdata/migrate"
)

func TestAddConstructorWithParamsStructRefactored(t *testing.T) {
	f := "abc"
	m := migrate.MigrateStruct{
		foo:    "foo",
		bar:    1,
		foobar: &f,
	}
	fmt.Println(m)
}
`
	err = os.WriteFile(filepath.Join(workDir, "migrate/struct.go"), []byte(structGoContent), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(workDir, "migrate/struct_test.go"), []byte(structTestGoContent), 0644)
	assert.NoError(t, err)

	ms := pachanger.NewMigrateStruct(workDir, targetPkg, suffix)
	err = ms.Migrate(filepath.Join(workDir, "migrate/struct_test.go"))
	assert.NoError(t, err)
	expectedStructTestGoContent := `package migrate_test

import (
	"fmt"
	"testing"

	"github.com/pyama86/pachanger/internal/pachanger/testdata/migrate"
)

func TestAddConstructorWithParamsStructRefactored(t *testing.T) {
	f := "abc"
	m := migrate.NewMigrateStructForTestMigrate(migrate.MigrateStructParamsForTestMigrate{Foo: "foo", Bar: 1, Foobar: &f})

	fmt.Println(m)
}
`
	actualStructTestGoContent, err := os.ReadFile(filepath.Join(workDir, "migrate/struct_test.go"))
	assert.NoError(t, err)
	assert.Equal(t, expectedStructTestGoContent, string(actualStructTestGoContent))
}
