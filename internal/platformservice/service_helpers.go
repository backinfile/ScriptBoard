package platformservice

func uninstallService(stopAndDisable, removeDefinitions, reload func() error) error {
	if err := stopAndDisable(); err != nil {
		return err
	}
	if err := removeDefinitions(); err != nil {
		return err
	}
	return reload()
}
