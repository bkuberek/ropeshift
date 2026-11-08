// Package api is the versioned HTTP surface every client shares (FR-API-3, FR-UI-4):
// its wire types and the one client. It imports only domain packages, so a client
// cannot reach the Store through it. The handlers live in internal/api/server.
package api
