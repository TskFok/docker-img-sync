package sync

import "context"

type Auth struct {
	Username string
	Password string
}

type Engine interface {
	Digest(ctx context.Context, ref string, auth *Auth) (string, error)
	Copy(ctx context.Context, src, dst string, srcAuth, dstAuth *Auth, progress func(phase string) error) error
}
