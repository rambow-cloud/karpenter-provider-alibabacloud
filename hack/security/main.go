// Copyright 2026 The Alibaba Cloud Karpenter Authors.
// SPDX-License-Identifier: Apache-2.0

// Check the effective selected versions, including replacements, against the
// security floors from the October 2026 audit. govulncheck handles new advisories.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"golang.org/x/mod/semver"
)

type module struct {
	Path, Version string
	Replace       *module
}

var minimumVersions = map[string]string{
	"github.com/google/cel-go":                          "v0.30.0",
	"github.com/klauspost/compress":                     "v1.18.7",
	"github.com/golang/glog":                            "v1.2.4",
	"github.com/yuin/goldmark":                          "v1.7.17",
	"golang.org/x/net":                                  "v0.60.0",
	"golang.org/x/crypto":                               "v0.57.0",
	"golang.org/x/text":                                 "v0.42.0",
	"golang.org/x/mod":                                  "v0.41.0",
	"golang.org/x/image":                                "v0.45.0",
	"google.golang.org/grpc":                            "v1.83.2",
	"go.opentelemetry.io/otel":                          "v1.45.0",
	"go.opentelemetry.io/otel/trace":                    "v1.45.0",
	"go.opentelemetry.io/otel/sdk":                      "v1.45.0",
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace": "v1.45.0",
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc": "v1.45.0",
	"go.etcd.io/etcd/api/v3":        "v3.6.14",
	"go.etcd.io/etcd/client/pkg/v3": "v3.6.14",
	"go.etcd.io/etcd/client/v3":     "v3.6.14",
	"go.etcd.io/etcd/pkg/v3":        "v3.6.14",
	"go.etcd.io/etcd/server/v3":     "v3.6.14",
}

func checkModules(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	found := 0
	for {
		var selected module
		if err := decoder.Decode(&selected); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("decode module list: %w", err)
		}
		found++
		floor, tracked := minimumVersions[selected.Path]
		if !tracked {
			continue
		}
		effective := selected
		if selected.Replace != nil {
			effective = *selected.Replace
		}
		if effective.Path != selected.Path || !semver.IsValid(effective.Version) {
			return fmt.Errorf("%s: cannot verify replacement %s %s", selected.Path, effective.Path, effective.Version)
		}
		if semver.Compare(effective.Version, floor) < 0 {
			return fmt.Errorf("%s: effective %s is below security floor %s", selected.Path, effective.Version, floor)
		}
	}
	if found == 0 {
		return fmt.Errorf("empty module list")
	}
	return nil
}

func main() {
	output, err := exec.Command("go", "list", "-m", "-json", "all").Output()
	if err == nil {
		err = checkModules(output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Effective dependency security floors passed. Removed modules need no version pin.")
}
