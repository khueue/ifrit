// Package check holds the few assertions the tests share.
package check

import (
	"slices"
	"strings"
	"testing"
)

// Equal reports a test error unless got equals want.
func Equal[T comparable](t testing.TB, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// Slice reports a test error unless got and want hold the same elements.
func Slice(t testing.TB, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n  got  %.200q\n  want %.200q", what, got, want)
	}
}

// Contains reports a test error for every want that is not part of got.
func Contains(t testing.TB, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, got)
		}
	}
}

// NoError stops the test if err is set.
func NoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ErrorContains reports a test error unless err is set and mentions every
// want.
func ErrorContains(t testing.TB, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("expected an error mentioning %q", wants)
		return
	}
	Contains(t, "error", err.Error(), wants...)
}
