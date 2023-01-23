//go:build !linux && !darwin
// +build !linux,!darwin

package usb

type SearchFilter struct{}

// Search returns nothing here for unsupported platforms.
func Search(filter SearchFilter, includeDevice func(vendorID, productID int) bool) []Description {
	return nil
}
