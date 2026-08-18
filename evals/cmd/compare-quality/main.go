package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/signaturekey/zephyr/evals/quality"
)

func main() {
	baseline := flag.String("baseline", "", "directory with old Zephyr eval records")
	candidate := flag.String("candidate", "", "directory with rewrite eval records")
	flag.Parse()
	if *baseline == "" || *candidate == "" {
		fmt.Fprintln(os.Stderr, "both --baseline and --candidate are required")
		os.Exit(2)
	}
	comparison, err := quality.CompareDirectories(*baseline, *candidate)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	printMetrics("baseline", comparison.Baseline)
	printMetrics("candidate", comparison.Candidate)
	if len(comparison.Regressions) == 0 {
		fmt.Println("quality regression: none")
		return
	}
	for _, regression := range comparison.Regressions {
		fmt.Printf("quality regression: %s\n", regression)
	}
	os.Exit(1)
}

func printMetrics(label string, metrics quality.Metrics) {
	fmt.Printf("%s: cases=%d recall=%.3f false_positive_rate=%.3f severity_agreement=%.3f\n",
		label, metrics.Cases, metrics.Recall(), metrics.FalsePositiveRate(), metrics.SeverityAgreement())
}
