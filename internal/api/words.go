package api

import (
	"fmt"
	"strconv"
)

// Dollars says an amount of money as every surface states it, the web UI's way: in dollars and cents,
// with an amount above zero that rounds to no cent read as under a cent rather than as nothing. A read
// model and its JSON keep every digit, and only the words round.
func Dollars(usd float64) string {
	if usd > 0 && usd < 0.005 {
		return "under $0.01"
	}
	return fmt.Sprintf("$%.2f", usd)
}

// Count says how many of something there are, in the singular for one: "1 batch of questions", and
// "2 batches of questions".
func Count(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(n, 10) + " " + many
}
