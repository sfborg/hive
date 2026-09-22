// mknotices writes the third-party notices shipped in hive release
// archives: the license and notice files of every module linked into
// hive on any release platform, the Go license, and the licenses of the
// vendored web assets. A module that states its license only in a README
// is covered by that README's License section. It exits with an error if a
// module has neither, so a release cannot ship incomplete notices.
//
// Not built into the hive binary. The release build runs it via
// .goreleaser.yaml.
//
// Usage:  go run ./tools/mknotices [-o FILE]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// platforms matches the release targets in .goreleaser.yaml. Dependencies
// differ slightly between them, so the notices cover their union.
var platforms = []struct{ goos, goarch string }{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

var (
	licenseName = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([._-].*)?$`)
	readmeName  = regexp.MustCompile(`(?i)^readme([._-].*)?$`)
	licenseHead = regexp.MustCompile(`(?i)^#+\s*licen[cs]e\b`)
)

type module struct{ path, version, dir string }

func main() {
	out := flag.String("o", "", "write to this file instead of standard output")
	flag.Parse()

	mods, err := linkedModules()
	if err != nil {
		fail(err)
	}

	var b bytes.Buffer
	b.WriteString("Third-party notices for hive\n\n")
	b.WriteString("hive includes the third-party software listed below. Each entry gives\n")
	b.WriteString("the module, its version, where its source can be downloaded, and its\n")
	b.WriteString("license and notice files as distributed.\n")

	for _, m := range mods {
		files, err := licenseFiles(m.dir)
		if err != nil {
			fail(fmt.Errorf("%s %s: %w", m.path, m.version, err))
		}
		if len(files) > 0 {
			err = section(&b, m.path+" "+m.version, "Source: "+proxyURL(m), files)
		} else {
			err = readmeLicenseSection(&b, m)
		}
		if err != nil {
			fail(err)
		}
	}

	goFiles, err := goLicenseFiles()
	if err != nil {
		fail(err)
	}
	gover, err := goEnv("GOVERSION")
	if err != nil {
		fail(err)
	}
	if err := section(&b, "Go standard library and runtime ("+gover+")",
		"Source: https://go.dev/dl/", goFiles); err != nil {
		fail(err)
	}
	if err := section(&b, "Lit (bundled in the web interface)",
		"Source: https://github.com/lit/dist",
		[]string{"internal/wui/dist/vendor/lit-3.x.x.min.js.LICENSE.txt"}); err != nil {
		fail(err)
	}

	b.WriteString("\n=== Trademarks ===\n\n")
	b.WriteString("ORCID™, the ORCID logo, and the iD logo are trademarks of ORCID, Inc.\n")
	b.WriteString("and are used in accordance with the ORCID Brand Guidelines:\n")
	b.WriteString("https://info.orcid.org/brand-guidelines/\n")

	if *out == "" {
		os.Stdout.Write(b.Bytes())
		return
	}
	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		fail(err)
	}
}

// linkedModules returns the non-main modules that provide packages linked
// into hive on any release platform, sorted by path.
func linkedModules() ([]module, error) {
	const format = `{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}{{end}}`
	seen := map[string]module{}
	for _, p := range platforms {
		cmd := exec.Command("go", "list", "-deps", "-f", format, ".")
		cmd.Env = append(os.Environ(), "GOOS="+p.goos, "GOARCH="+p.goarch, "CGO_ENABLED=0")
		cmd.Stderr = os.Stderr
		outb, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s/%s: %w", p.goos, p.goarch, err)
		}
		for line := range strings.SplitSeq(string(outb), "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 3 {
				continue
			}
			if f[2] == "" {
				return nil, fmt.Errorf("%s %s: module not downloaded", f[0], f[1])
			}
			seen[f[0]] = module{path: f[0], version: f[1], dir: f[2]}
		}
	}
	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].path < mods[j].path })
	return mods, nil
}

// licenseFiles returns the license and notice files at a module's root.
func licenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && licenseName.MatchString(e.Name()) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

// readmeLicenseSection covers a module that states its license only in a
// README: it reproduces that README's License section as written.
func readmeLicenseSection(b *bytes.Buffer, m module) error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !readmeName.MatchString(e.Name()) {
			continue
		}
		text, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			return err
		}
		var lines []string
		in := false
		for line := range strings.SplitSeq(string(text), "\n") {
			if licenseHead.MatchString(line) {
				in = true
				continue
			}
			if in && strings.HasPrefix(line, "#") {
				break
			}
			if in {
				lines = append(lines, line)
			}
		}
		stated := strings.TrimSpace(strings.Join(lines, "\n"))
		if stated == "" {
			continue
		}
		fmt.Fprintf(b, "\n=== %s %s ===\nSource: %s\n", m.path, m.version, proxyURL(m))
		fmt.Fprintf(b, "\n--- %s, License section (the module has no license file) ---\n\n%s\n", e.Name(), stated)
		return nil
	}
	return fmt.Errorf("%s %s: no license file and no license statement in a README", m.path, m.version)
}

func section(b *bytes.Buffer, title, source string, files []string) error {
	fmt.Fprintf(b, "\n=== %s ===\n%s\n", title, source)
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "\n--- %s ---\n\n%s\n", filepath.Base(f), strings.TrimRight(string(text), "\n"))
	}
	return nil
}

// proxyURL is the module's source archive on the Go module proxy.
func proxyURL(m module) string {
	return "https://proxy.golang.org/" + escape(m.path) + "/@v/" + escape(m.version) + ".zip"
}

// escape applies the module proxy's case encoding: each upper-case letter
// becomes '!' followed by its lower-case form.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// goLicenseFiles returns the Go distribution's LICENSE, plus PATENTS when
// present. Official Go releases keep them in GOROOT; some Linux packages
// move LICENSE to /usr/share/licenses/go.
func goLicenseFiles() ([]string, error) {
	goroot, err := goEnv("GOROOT")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range []string{
		filepath.Join(goroot, "LICENSE"),
		"/usr/share/licenses/go/LICENSE",
	} {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
			break
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("Go LICENSE not found in %s", goroot)
	}
	if _, err := os.Stat(filepath.Join(goroot, "PATENTS")); err == nil {
		files = append(files, filepath.Join(goroot, "PATENTS"))
	}
	return files, nil
}

func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mknotices:", err)
	os.Exit(1)
}
