// Build the Windows coordinator and both supported Linux helper architectures.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if e := build(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func build() error {
	if _, e := os.Stat("go.mod"); e != nil {
		return fmt.Errorf("run this build command from the repository root")
	}
	checksums := map[string]string{}
	for _, target := range []struct{ os, arch, path string }{{"windows", "amd64", "bin/muminovic-cloud-update.exe"}, {"linux", "amd64", "bin/linux-amd64/muminovic-cloud-update"}, {"linux", "arm64", "bin/linux-arm64/muminovic-cloud-update"}} {
		if e := os.MkdirAll(filepath.Dir(target.path), 0755); e != nil {
			return e
		}
		command := exec.Command("go", "build", "-trimpath", "-o", target.path, "./cmd/nextcloud-update")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GOOS=") && !strings.HasPrefix(entry, "GOARCH=") && !strings.HasPrefix(entry, "CGO_ENABLED=") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "GOOS="+target.os, "GOARCH="+target.arch, "CGO_ENABLED=0")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		fmt.Println("Building", target.os+"/"+target.arch)
		if e := command.Run(); e != nil {
			return e
		}
		file, e := os.Open(target.path)
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, file)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		checksums[target.path] = hex.EncodeToString(hash.Sum(nil))
	}
	data, e := json.MarshalIndent(checksums, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile("bin/checksums.json", append(data, '\n'), 0644)
}
