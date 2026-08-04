package websitemonitor

import (
	"reflect"
	"testing"
)

func TestParseHTTPStatusRangesAcceptsSingleCodesAndRanges(t *testing.T) {
	got, err := ParseHTTPStatusRanges("200;401-499;503")
	if err != nil {
		t.Fatalf("parse status ranges: %v", err)
	}
	want := []HTTPStatusRange{
		{Start: 200, End: 200},
		{Start: 401, End: 499},
		{Start: 503, End: 503},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status ranges = %#v, want %#v", got, want)
	}
	if formatted := FormatHTTPStatusRanges(got); formatted != "200;401-499;503" {
		t.Fatalf("formatted status ranges = %q", formatted)
	}
}

func TestParseHTTPStatusRangesRejectsInvalidRanges(t *testing.T) {
	for _, input := range []string{"", "99", "600", "499-401", "200-", "200-299-399", "ok"} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseHTTPStatusRanges(input); err == nil {
				t.Fatalf("invalid status rule %q was accepted", input)
			}
		})
	}
}

func TestAcceptedHTTPStatusSupportsConfiguredRangesAndAnyResponse(t *testing.T) {
	custom := Config{
		HTTPSuccessMode: HTTPSuccessExact,
		ExpectedStatusRanges: []HTTPStatusRange{
			{Start: 200, End: 200},
			{Start: 401, End: 499},
			{Start: 503, End: 503},
		},
	}
	for _, status := range []int{200, 401, 450, 499, 503} {
		if !acceptedHTTPStatus(custom, status) {
			t.Errorf("custom status %d was rejected", status)
		}
	}
	for _, status := range []int{199, 201, 400, 500, 502, 504} {
		if acceptedHTTPStatus(custom, status) {
			t.Errorf("unexpected custom status %d was accepted", status)
		}
	}
	if !acceptedHTTPStatus(Config{HTTPSuccessMode: HTTPSuccessAnyResponse}, 503) {
		t.Fatal("any-response mode rejected an HTTP response")
	}
	if !acceptedHTTPStatus(Config{HTTPSuccessMode: HTTPSuccessExact, ExpectedStatuses: []int{204}}, 204) {
		t.Fatal("legacy exact-status configuration no longer matches")
	}
}

func TestNormalizeConfigValidatesStatusRangesAndAnyResponse(t *testing.T) {
	custom, err := normalizeConfig(Config{
		Name: "Custom status rules", Kind: KindHTTP, URL: "https://example.com/",
		HTTPSuccessMode:      HTTPSuccessExact,
		ExpectedStatusRanges: []HTTPStatusRange{{Start: 200, End: 299}},
	})
	if err != nil {
		t.Fatalf("normalize custom ranges: %v", err)
	}
	if len(custom.ExpectedStatusRanges) != 1 || custom.ExpectedStatusRanges[0].End != 299 {
		t.Fatalf("normalized ranges = %#v", custom.ExpectedStatusRanges)
	}

	if _, err := normalizeConfig(Config{
		Name: "Invalid status rules", Kind: KindHTTP, URL: "https://example.com/",
		HTTPSuccessMode:      HTTPSuccessExact,
		ExpectedStatusRanges: []HTTPStatusRange{{Start: 500, End: 499}},
	}); err == nil {
		t.Fatal("descending status range was accepted")
	}

	anyResponse, err := normalizeConfig(Config{
		Name: "Any response", Kind: KindHTTP, URL: "https://example.com/",
		HTTPSuccessMode:      HTTPSuccessAnyResponse,
		ExpectedStatusRanges: []HTTPStatusRange{{Start: 200, End: 299}},
	})
	if err != nil {
		t.Fatalf("normalize any-response mode: %v", err)
	}
	if len(anyResponse.ExpectedStatusRanges) != 0 {
		t.Fatalf("any-response mode retained stale status ranges: %#v", anyResponse.ExpectedStatusRanges)
	}
}
