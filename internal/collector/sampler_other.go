//go:build !linux && !windows

package collector

func (s *Sampler) tick() error {
	return nil
}

func (s *Sampler) refreshSlow() error {
	return nil
}
