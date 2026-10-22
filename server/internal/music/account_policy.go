package music

func publicTrack(track Track, auth authContext) Track {
	if auth.role != "admin" {
		track.Path = ""
	}
	return track
}
