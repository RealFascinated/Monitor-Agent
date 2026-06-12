package collector

func (s *Sampler) tick() error {
	update, err := s.backend.Tick(s.Ready())
	if err != nil {
		return err
	}
	if update.Skip {
		return nil
	}
	s.mergeTick(update)
	return nil
}

func (s *Sampler) refreshSlow() error {
	update, err := s.backend.RefreshSlow()
	if err != nil {
		return err
	}
	s.mergeSlow(update)
	return nil
}
