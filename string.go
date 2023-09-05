package utils

import (
	"crypto/rand"
	"math"
	"math/big"
	"strings"
)

const alphaLowers string = "abcdefghijklmnopqrstuvwxyz"

var (
	alphaUppers     = strings.ToUpper(alphaLowers)
	maxInt32        = big.NewInt(math.MaxInt32)
	fiftyFityChance = big.NewInt(2)
)

// RandomAlphaString returns a random alphabetic string of the given size.
// Note(erd): all random strings are subject to modulus bias; hope that
// does not matter to you.
func RandomAlphaString(size int) string {
	if size < 0 {
		return ""
	}
	chars := make([]byte, 0, size)
	for i := 0; i < size; i++ {
		valBig, err := rand.Int(rand.Reader, maxInt32)
		if err != nil {
			panic(err)
		}
		val := int(valBig.Int64())
		chance, err := rand.Int(rand.Reader, fiftyFityChance)
		if err != nil {
			panic(err)
		}
		switch chance.Int64() {
		case 0:
			chars = append(chars, alphaLowers[val%len(alphaLowers)])
		case 1:
			chars = append(chars, alphaUppers[val%len(alphaUppers)])
		}
	}
	return string(chars)
}

// StringSet represents a mathematical set of string.
type StringSet map[string]struct{}

// NewStringSet returns a new string set from the given series of values
// where duplicates are okay.
func NewStringSet(values ...string) StringSet {
	set := make(StringSet, len(values))
	for _, val := range values {
		set[val] = struct{}{}
	}
	return set
}

// StringSliceRemove removes an element from the slice at the given position.
func StringSliceRemove(from []string, at int) []string {
	if at >= len(from) {
		return from
	}
	return append(from[:at], from[at+1:]...)
}
