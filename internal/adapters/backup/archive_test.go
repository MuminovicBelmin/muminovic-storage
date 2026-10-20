package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"nextcloud-updater/internal/adapters/filesystem"
	"nextcloud-updater/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

type dumper struct {
	data []byte
	err  error
}

func (d dumper) DumpDatabase(_ context.Context, _ domain.Deployment, w io.Writer) error {
	if d.err != nil {
		return d.err
	}
	_, e := w.Write(d.data)
	return e
}
func TestArchiveIncludesSettingsKeysAndNoUnselectedPayload(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	keys := filepath.Join(root, "files_encryption")
	os.Mkdir(config, 0700)
	os.Mkdir(keys, 0700)
	os.WriteFile(filepath.Join(config, "config.php"), []byte("private config"), 0600)
	os.WriteFile(filepath.Join(keys, "key"), []byte("private key"), 0600)
	os.WriteFile(filepath.Join(root, "heavy-payload"), []byte("excluded"), 0600)
	archive := filepath.Join(root, "settings.tar.gz")
	paths := []domain.ArchivePath{{Source: config, Archive: "nextcloud/config"}, {Source: keys, Archive: "encryption-keys"}}
	if e := WriteArchive(context.Background(), archive, paths); e != nil {
		t.Fatal(e)
	}
	if e := ValidateArchive(archive); e != nil {
		t.Fatal(e)
	}
	f, _ := os.Open(archive)
	defer f.Close()
	gz, _ := gzip.NewReader(f)
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		names[h.Name] = true
	}
	if !names["nextcloud/config/config.php"] || !names["encryption-keys/key"] || names["heavy-payload"] {
		t.Fatal(names)
	}
}
func TestRealDatabaseBackupAndChecksumGate(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "before-31")
	os.Mkdir(stage, 0700)
	source := filepath.Join(root, "compose.yml")
	os.WriteFile(source, []byte("services: {}\n"), 0600)
	service := Remote{RunDirectory: root, Dumper: dumper{data: []byte("CREATE TABLE test(id INT);\n")}}
	b, e := service.Create(context.Background(), domain.Deployment{Root: root, Source: source}, domain.BackupInput{Metadata: map[string]string{"scope": "test"}}, stage, "30.0.17", "31.0.14")
	if e != nil {
		t.Fatal(e)
	}
	if e = service.Verify(b); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSQL(filepath.Join(stage, "database.sql.gz")); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(stage, "database.sql.gz"), []byte("truncated"), 0600)
	if e = service.Verify(b); e == nil {
		t.Fatal("corrupt dump accepted")
	}
	if e = service.Clear(filepath.Join(t.TempDir(), "other")); e == nil {
		t.Fatal("cleared an unrelated directory")
	}
}
func TestEmptyOrCorruptSQLCannotPass(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("SQL")} {
		var b bytes.Buffer
		g := gzip.NewWriter(&b)
		g.Write(data)
		g.Close()
		file := filepath.Join(t.TempDir(), "dump.gz")
		os.WriteFile(file, b.Bytes(), 0600)
		e := ValidateSQL(file)
		if len(data) == 0 && e == nil {
			t.Fatal("accepted empty SQL")
		}
		if len(data) > 0 && e != nil {
			t.Fatal(e)
		}
		os.WriteFile(file, b.Bytes()[:b.Len()-3], 0600)
		if ValidateSQL(file) == nil {
			t.Fatal("accepted truncated gzip")
		}
	}
}
func TestManifestRequiresEveryArtifactAndRejectsTraversal(t *testing.T) {
	m := domain.Manifest{Artifacts: []domain.Artifact{{Name: "../../secret", SHA256: "x"}}}
	if ValidateManifest(m) == nil {
		t.Fatal("unsafe manifest accepted")
	}
	if ValidateManifest(domain.Manifest{}) == nil {
		t.Fatal("empty manifest accepted")
	}
}
func TestManifestTamperingCannotPass(t *testing.T) {
	dir := t.TempDir()
	filesystem.WriteJSON(filepath.Join(dir, "manifest.json"), domain.Manifest{})
	hash, _ := filesystem.Hash(filepath.Join(dir, "manifest.json"))
	b := domain.Backup{SHA256: hash}
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("changed"), 0600)
	if VerifyFiles(dir, b) == nil {
		t.Fatal("manifest tampering accepted")
	}
}
