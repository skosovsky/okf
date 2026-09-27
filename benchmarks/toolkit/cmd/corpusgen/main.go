package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skosovsky/okf/benchmarks/toolkit"
)

func main() {
	root := flag.String("out", "", "output directory")
	large := flag.Bool("large", false, "include the 10,000-concept manual corpus")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}
	sizes := []int{10, 100, 1000}
	if *large {
		sizes = append(sizes, 10000)
	}
	for _, n := range sizes {
		name := filepath.Join(*root, fmt.Sprintf("n%d", n))
		corpus, manifest := toolkit.Generate(n)
		if err := corpus.WriteDir(name); err != nil {
			panic(err)
		}
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(*root, fmt.Sprintf("n%d.manifest.json", n)), append(data, '\n'), 0644); err != nil {
			panic(err)
		}
		fmt.Printf("n=%d sha256=%s bytes=%d files=%d\n", n, manifest.SHA256, manifest.Bytes, manifest.Files)
	}
	for _, special := range []struct {
		name string
		make func() (toolkit.Corpus, toolkit.Manifest)
	}{
		{"interop", toolkit.InteropCorpus},
		{"temporal-date", func() (toolkit.Corpus, toolkit.Manifest) { return toolkit.TemporalCorpus(false) }},
		{"temporal-instant", func() (toolkit.Corpus, toolkit.Manifest) { return toolkit.TemporalCorpus(true) }},
	} {
		corpus, manifest := special.make()
		if err := corpus.WriteDir(filepath.Join(*root, special.name)); err != nil {
			panic(err)
		}
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(*root, special.name+".manifest.json"), append(data, '\n'), 0644); err != nil {
			panic(err)
		}
		fmt.Printf("%s sha256=%s bytes=%d files=%d\n", special.name, manifest.SHA256, manifest.Bytes, manifest.Files)
	}
}
