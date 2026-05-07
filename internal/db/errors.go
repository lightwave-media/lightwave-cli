package db

import (
	"errors"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
)

// WrapConnectError converts a low-level pgx/network connection failure into
// a multi-line, actionable message. Non-connection errors pass through
// unchanged. The original error is always wrapped via %w so callers can
// errors.Is / errors.As against it.
func WrapConnectError(err error, host string, port int) error {
	if err == nil {
		return nil
	}
	if !isConnectFailure(err) {
		return err
	}

	return fmt.Errorf(`Cannot connect to platform database at %s:%d.

Likely fix:
  1. Run `+"`lw dev start`"+` to bring up the Docker stack
  2. OR start the local mirror: `+"`brew services start postgresql@14`"+`
  3. OR set LW_DB_URL to point at a different DSN

Original error: %w`, host, port, err)
}

// isConnectFailure reports whether err is a pgx / net dial-time failure
// (refused / unreachable / DNS), as opposed to e.g. a query-time error.
func isConnectFailure(err error) bool {
	var pgErr *pgconn.ConnectError
	if errors.As(err, &pgErr) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	return false
}
