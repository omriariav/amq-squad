package cli

import "os"

// readDeliveryReceipt is intentionally test-only. Production receipt reads go
// through readDeliveryReceiptAt on an os.Root directory handle.
func readDeliveryReceipt(path string) (deliveryReceiptData, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return deliveryReceiptData{}, err
	}
	return decodeDeliveryReceipt(b, path)
}
