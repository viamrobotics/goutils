package serial

// checkProductDeviceIDs returns the Type that corresponds to the given
// vendor and product IDs.
// Note(erd): This probably should be based off registration or some user
// supplied check function.
func checkProductDeviceIDs(vendorID, productID int) Type {
	if vendorID == 0x2341 {
		return TypeArduino
	}
	if vendorID == 0x2a19 {
		// Numato device, different gpio counts have different product IDs
		if productID == 0x0802 || productID == 0x0805 || productID == 0x800 || productID == 0xC05 {
			return TypeNumatoGPIO
		}
	}
	return TypeUnknown
}
