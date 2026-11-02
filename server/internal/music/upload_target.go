package music

func validateResumeTarget(reader sqlReader, owner string, target *string) error {
	if target == nil || *target == "" {
		return nil
	}
	var allowed bool
	if err := reader.QueryRow("SELECT EXISTS(SELECT 1 FROM playlists WHERE id=? AND owner_id=? AND system=0)", *target, owner).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errResourceHidden
	}
	return nil
}
