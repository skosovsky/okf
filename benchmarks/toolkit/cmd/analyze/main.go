// Command analyze summarizes raw Go benchmark records. It intentionally uses
// only the standard library, so the analysis code is pinned with the toolkit.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type sample struct{ ns, bytes, allocs float64 }

func main() {
	baseline := flag.String("baseline", "", "raw baseline go test output")
	corrected := flag.String("corrected", "", "optional raw corrected output")
	flag.Parse()
	if *baseline == "" {
		fmt.Fprintln(os.Stderr, "-baseline is required")
		os.Exit(2)
	}
	base, err := read(*baseline)
	if err != nil {
		panic(err)
	}
	var current map[string][]sample
	if *corrected != "" {
		current, err = read(*corrected)
		if err != nil {
			panic(err)
		}
	}
	names := workloadNames(base, current)
	fmt.Println("| Workload | Baseline N | Baseline ns/op | Baseline B/op | Baseline allocs/op | Corrected N | Corrected ns/op | Corrected B/op | Corrected allocs/op | Δ time | Δ bytes | Δ allocs |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, name := range names {
		oldRow := summarize(base[name])
		newRow := summarize(current[name])
		fmt.Printf("| `%s` | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			name, oldRow.n, oldRow.nsText, oldRow.bytesText, oldRow.allocsText,
			newRow.n, newRow.nsText, newRow.bytesText, newRow.allocsText,
			delta(oldRow.ns, newRow.ns, oldRow.n != "N/A" && newRow.n != "N/A"),
			delta(oldRow.bytes, newRow.bytes, oldRow.n != "N/A" && newRow.n != "N/A"),
			delta(oldRow.allocs, newRow.allocs, oldRow.n != "N/A" && newRow.n != "N/A"))
	}
}

func workloadNames(base, current map[string][]sample) []string {
	names := make([]string, 0, len(base)+len(current))
	for name := range base {
		names = append(names, name)
	}
	for name := range current {
		if _, ok := base[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

type summary struct {
	n, nsText, bytesText, allocsText string
	ns, bytes, allocs                float64
}

func summarize(rows []sample) summary {
	if len(rows) == 0 {
		return summary{n: "N/A", nsText: "N/A", bytesText: "N/A", allocsText: "N/A"}
	}
	if len(rows) != 5 {
		panic(fmt.Sprintf("workload has %d samples, want 5", len(rows)))
	}
	ns, nsMAD := aggregate(rows, func(s sample) float64 { return s.ns })
	mem, memMAD := aggregate(rows, func(s sample) float64 { return s.bytes })
	allocs, allocMAD := aggregate(rows, func(s sample) float64 { return s.allocs })
	return summary{
		n: strconv.Itoa(len(rows)), nsText: fmt.Sprintf("%.0f ± %.0f", ns, nsMAD),
		bytesText:  fmt.Sprintf("%.0f ± %.0f", mem, memMAD),
		allocsText: fmt.Sprintf("%.0f ± %.0f", allocs, allocMAD),
		ns:         ns, bytes: mem, allocs: allocs,
	}
}

func delta(before, after float64, comparable bool) string {
	if !comparable || before == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%+.1f%%", 100*(after/before-1))
}

func read(path string) (map[string][]sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := make(map[string][]sample)
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) < 8 || !strings.HasPrefix(fields[0], "BenchmarkToolkit/") {
			continue
		}
		ns, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, err
		}
		mem, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			return nil, err
		}
		allocs, err := strconv.ParseFloat(fields[6], 64)
		if err != nil {
			return nil, err
		}
		if fields[3] != "ns/op" || fields[5] != "B/op" || fields[7] != "allocs/op" {
			return nil, fmt.Errorf("unrecognized units: %v", fields)
		}
		out[fields[0]] = append(out[fields[0]], sample{ns, mem, allocs})
	}
	return out, scan.Err()
}

func aggregate(samples []sample, selectValue func(sample) float64) (float64, float64) {
	values := make([]float64, len(samples))
	for i, s := range samples {
		values[i] = selectValue(s)
	}
	sort.Float64s(values)
	median := values[len(values)/2]
	for i := range values {
		if values[i] > median {
			values[i] -= median
		} else {
			values[i] = median - values[i]
		}
	}
	sort.Float64s(values)
	return median, values[len(values)/2]
}
