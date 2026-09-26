// Command paired interleaves prebuilt baseline and corrected Go benchmark
// binaries. Building either binary is the caller's responsibility and stays
// outside every timed workload.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type arm struct {
	name, testBinary, cliBinary string
	corrected                   bool
	output                      *os.File
}

func main() {
	baselineTest := flag.String("baseline-test", "", "prebuilt baseline toolkit.test")
	baselineCLI := flag.String("baseline-cli", "", "prebuilt baseline okf CLI")
	correctedTest := flag.String("corrected-test", "", "prebuilt corrected toolkit.test")
	correctedCLI := flag.String("corrected-cli", "", "prebuilt corrected okf CLI")
	outDir := flag.String("out", "", "directory for two raw output files")
	benchTime := flag.String("benchtime", "100ms", "identical Go benchmark duration for both arms")
	flag.Parse()
	if *baselineTest == "" || *baselineCLI == "" || *correctedTest == "" || *correctedCLI == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "all binary paths and -out are required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		panic(err)
	}
	baseOut, err := os.Create(filepath.Join(*outDir, "baseline.raw.txt"))
	if err != nil {
		panic(err)
	}
	defer baseOut.Close()
	correctedOut, err := os.Create(filepath.Join(*outDir, "corrected.raw.txt"))
	if err != nil {
		panic(err)
	}
	defer correctedOut.Close()
	base := arm{name: "baseline", testBinary: *baselineTest, cliBinary: *baselineCLI, output: baseOut}
	current := arm{name: "corrected", testBinary: *correctedTest, cliBinary: *correctedCLI, corrected: true, output: correctedOut}
	// Five observations per arm, balanced in adjacent AB and BA pairs.
	for index, selected := range []arm{base, current, current, base, base, current, current, base, base, current} {
		fmt.Fprintf(os.Stderr, "%02d/10 %s\n", index+1, selected.name)
		if err := run(selected, *benchTime); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func run(selected arm, benchTime string) error {
	args := []string{"-test.run=^$", "-test.bench=^BenchmarkToolkit$", "-test.benchtime=" + benchTime, "-test.count=1", "-test.timeout=30m"}
	cmd := exec.Command(selected.testBinary, args...)
	env := append(os.Environ(), "OKF_BENCH_CLI="+selected.cliBinary, "OKF_BENCH_LARGE=0")
	if selected.corrected {
		env = append(env, "OKF_BENCH_EXPECT_CORRECTED=1")
	} else {
		env = append(env, "OKF_BENCH_EXPECT_CORRECTED=0")
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s benchmark failed after %s: %w\n%s\n%s", selected.name, time.Since(started), err, stderr.String(), stdout.String())
	}
	if _, err := selected.output.Write(stdout.Bytes()); err != nil {
		return err
	}
	if err := selected.output.Sync(); err != nil {
		return err
	}
	return nil
}
