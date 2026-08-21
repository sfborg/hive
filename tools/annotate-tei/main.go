// annotate-tei is a scratch CLI for eyeballing the annotator's
// output against a real GROBID TEI file. Point it at a .tei.xml
// and it writes the augmented XML to stdout (or a file with -o).
// Useful for iterating on the annotator before wiring it into the
// server; not shipped in the hive binary.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sfborg/hive/pkg/annotator"
)

func main() {
	out := flag.String("o", "", "output path (default: stdout)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: annotate-tei [-o out.xml] input.tei.xml")
		os.Exit(2)
	}
	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	a := annotator.New()
	augmented, err := a.FindNames(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "annotate:", err)
		os.Exit(1)
	}
	if *out == "" {
		os.Stdout.Write(augmented)
		return
	}
	if err := os.WriteFile(*out, augmented, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
}
