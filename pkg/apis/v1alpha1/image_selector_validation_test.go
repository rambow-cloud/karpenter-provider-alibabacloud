package v1alpha1

import (
	"testing"
)

func TestImageSelectorsRejectInvalidQueries(t *testing.T) {
	tests := map[string][]ImageSelectorTerm{
		"missing terms":             nil,
		"empty term":                {{}},
		"empty tags":                {{Tags: map[string]string{}}},
		"empty value":               {{Tags: map[string]string{"env": ""}}},
		"empty key":                 {{Tags: map[string]string{"": "prod"}}},
		"empty name":                {{Name: ptrForUnit("")}},
		"blank family":              {{ImageFamily: ptrForUnit("  ")}},
		"invalid alias":             {{ImageOwnerAlias: ptrForUnit("public")}},
		"mixed ID":                  {{ID: ptrForUnit("m-test"), Name: ptrForUnit("app")}},
		"invalid ID":                {{ID: ptrForUnit("not-an-image")}},
		"missing owner alias":       {{Name: ptrForUnit("app"), ImageOwnerID: ptrForUnit("123456")}},
		"self owner":                {{ImageOwnerAlias: ptrForUnit("self"), ImageOwnerID: ptrForUnit("123456")}},
		"system owner":              {{ImageOwnerAlias: ptrForUnit("system"), ImageOwnerID: ptrForUnit("123456")}},
		"marketplace owner":         {{ImageOwnerAlias: ptrForUnit("marketplace"), ImageOwnerID: ptrForUnit("123456")}},
		"overflow owner":            {{ImageOwnerAlias: ptrForUnit("others"), ImageOwnerID: ptrForUnit("9223372036854775808")}},
		"twenty digit owner":        {{ImageOwnerAlias: ptrForUnit("others"), ImageOwnerID: ptrForUnit("10000000000000000000")}},
		"leading zero owner":        {{ImageOwnerAlias: ptrForUnit("others"), ImageOwnerID: ptrForUnit("0123456")}},
		"valid followed by invalid": {{ID: ptrForUnit("m-valid")}, {}},
	}
	for name, terms := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateImageSelectorTerms(terms); err == nil {
				t.Fatal("invalid selector was accepted")
			}
		})
	}
	for _, terms := range [][]ImageSelectorTerm{
		{{ID: ptrForUnit("m-test")}},
		{{ID: ptrForUnit("lifsea_3_x64_5G_alibase_20260519.qcow2")}},
		{{ImageOwnerAlias: ptrForUnit("others"), ImageOwnerID: ptrForUnit("9223372036854775807")}},
		{{Name: ptrForUnit("app-*"), ImageFamily: ptrForUnit("app"), Tags: map[string]string{"env": "prod"}}},
	} {
		if err := ValidateImageSelectorTerms(terms); err != nil {
			t.Fatalf("valid query rejected: %v", err)
		}
	}
}
