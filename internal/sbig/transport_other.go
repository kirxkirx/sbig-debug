//go:build !windows

package sbig

import "errors"

func loadTransport(string) (transport, error) {
	return nil, errors.New("camera access requires Windows and the installed SBIG Universal Driver; use simulation on this platform")
}
