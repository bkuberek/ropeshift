package ports

// Mailbox has no fake.
type Mailbox interface{ Send() error }
