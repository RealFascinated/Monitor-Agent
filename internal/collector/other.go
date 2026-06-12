//go:build !linux && !windows

package collector

func collect(opts Options) (Result, error) {
	return Result{}, nil
}
