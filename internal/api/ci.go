package api

import (
	"context"
	"net/http"
)

// CIWait adds time to the CI timeout of a work order's head, when it has passed it: the time named, or one more p85
// of the repository's CI.
func (c *ProjectClient) CIWait(ctx context.Context, id string, in CIWait) (CIWaited, error) {
	var out CIWaited
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "ci-wait"), in, &out)
	return out, err
}

// CIRerun records a rerun of the checks of a work order's head, when its CI has not finished or ended in an
// infrastructure failure. It starts no agent.
func (c *ProjectClient) CIRerun(ctx context.Context, id string) (CIRerun, error) {
	var out CIRerun
	err := c.do(ctx, http.MethodPost, workOrderPath(id, "ci-rerun"), struct{}{}, &out)
	return out, err
}
