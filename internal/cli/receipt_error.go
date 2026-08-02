package cli

import "fmt"

const receiptCorruptRecovery = "preserve the receipt artifact and do not retry delivery; from the selected project, inspect it with `amq-squad receipt show <message-id> --json` using the receipt's recorded message_id before manual reconciliation"

// receiptCorruptf owns the stable machine-consumed prefix and the operator
// recovery for every corrupt durable-receipt refusal. Consumers rely on the
// prefix, so the actionable suffix must be additive.
func receiptCorruptf(format string, args ...any) error {
	return fmt.Errorf("receipt_corrupt: "+format+"; remedy: "+receiptCorruptRecovery, args...)
}
