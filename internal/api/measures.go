package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/measures"
)

// MeasuresPath is the product measures' read.
const MeasuresPath = "/v1/measures"

// DefaultMeasureWindow is the window a read that names none covers.
const DefaultMeasureWindow = "7d"

// ParseWindow reads a window as a person writes one: a Go duration such as `24h`, or a number of days
// such as `7d`.
func ParseWindow(text string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(text, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("window %q is not a number of days", text)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("window %q is not a duration such as 24h or 7d", text)
	}
	return d, nil
}

// Measures reads every product measure over one window, from the ledger alone.
func (c *Client) Measures(ctx context.Context, window string) (measures.Report, error) {
	var out measures.Report
	path := MeasuresPath
	if window != "" {
		path += "?window=" + url.QueryEscape(window)
	}
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
