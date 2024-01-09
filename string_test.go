package utils

import (
	"fmt"
	"testing"

	"go.viam.com/test"
)

func TestRandomAlphaString(t *testing.T) {
	for _, tc := range []int{-1, 0, 1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("size %d", tc), func(t *testing.T) {
			str := RandomAlphaString(tc)
			if tc <= 0 {
				test.That(t, str, test.ShouldBeEmpty)
				return
			}
			test.That(t, str, test.ShouldHaveLength, tc)
		})
	}
}

func TestStringSet(t *testing.T) {
	ss := NewStringSet("foo")
	_, ok := ss["foo"]
	test.That(t, ok, test.ShouldBeTrue)

	// Adding a new value
	ss.Add("bar")

	_, ok = ss["bar"]
	test.That(t, ok, test.ShouldBeTrue)
	_, ok = ss["foo"]
	test.That(t, ok, test.ShouldBeTrue)

	// Removing a value
	ss.Remove("bar")
	_, ok = ss["bar"]
	test.That(t, ok, test.ShouldBeFalse)
	_, ok = ss["foo"]
	test.That(t, ok, test.ShouldBeTrue)

	// Removing a value that doesn't exist
	ss.Remove("no-op")
	_, ok = ss["foo"]
	test.That(t, ok, test.ShouldBeTrue)
}

func TestStringSliceRemove(t *testing.T) {
	for idx, tc := range []struct {
		In  []string
		At  int
		Out []string
	}{
		{[]string{}, 0, []string{}},
		{[]string{}, 1, []string{}},
		{[]string{"1"}, 1, []string{"1"}},
		{[]string{"1", "2"}, 1, []string{"1"}},
		{[]string{"1", "2", "3", "4"}, 2, []string{"1", "2", "4"}},
	} {
		t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
			test.That(t, StringSliceRemove(tc.In, tc.At), test.ShouldResemble, tc.Out)
		})
	}
}
