// Package kubernetesversion defines the provider's minimum Kubernetes version.
package kubernetesversion

import (
	"fmt"
	"regexp"
	"strings"

	utilversion "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apimachinery/pkg/version"
)

const Minimum = "1.34"

var minorVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+$`)

// Validate accepts a minor version or a semantic version, including ACK suffixes.
// Only the numeric release determines the minimum; an ACK suffix is not an
// older Kubernetes release. Passing this check does not establish runtime compatibility.
func Validate(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("Kubernetes version is required; specify a version >= %s", Minimum)
	}
	parseValue := strings.TrimSpace(value)
	if minorVersion.MatchString(parseValue) {
		parseValue += ".0"
	}
	v, err := utilversion.ParseSemantic(parseValue)
	if err != nil {
		return fmt.Errorf("invalid Kubernetes version %q: %w", value, err)
	}
	if v.Major() < 1 || (v.Major() == 1 && v.Minor() < 34) {
		return fmt.Errorf("Kubernetes %s is unsupported; requires Kubernetes >= %s", value, Minimum)
	}
	return nil
}

type ServerVersionReader interface {
	ServerVersion() (*version.Info, error)
}

// ValidateServer checks the actual API server instead of a configured target.
func ValidateServer(reader ServerVersionReader) error {
	info, err := reader.ServerVersion()
	if err != nil {
		return fmt.Errorf("discover Kubernetes server version: %w", err)
	}
	if info == nil {
		return fmt.Errorf("Kubernetes server returned no version information")
	}
	return Validate(info.GitVersion)
}
