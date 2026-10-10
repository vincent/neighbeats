package store

import "context"

// FormatCount is the number of tracks of one file format.
type FormatCount struct {
	Format string
	Count  int
}

// Stats summarises the library index.
type Stats struct {
	Tracks         int
	Formats        []FormatCount // most common first
	WithRecording  int           // tracks with a MusicBrainz recording id
	WithArtist     int
	WithRelease    int
	WithTitle      int
	WithArtistName int
	TagErrors      int
	Artists        int // distinct non-empty artist MBIDs
	Albums         int // distinct non-empty release MBIDs
}

// Stats computes the summary in a handful of queries.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
	COALESCE(SUM(mbid_recording <> ''), 0),
	COALESCE(SUM(mbid_artist <> ''), 0),
	COALESCE(SUM(mbid_release <> ''), 0),
	COALESCE(SUM(title <> ''), 0),
	COALESCE(SUM(artist <> ''), 0),
	COALESCE(SUM(tag_error <> ''), 0),
	COUNT(DISTINCT NULLIF(mbid_artist, '')),
	COUNT(DISTINCT NULLIF(mbid_release, ''))
FROM tracks`).Scan(&st.Tracks, &st.WithRecording, &st.WithArtist, &st.WithRelease,
		&st.WithTitle, &st.WithArtistName, &st.TagErrors, &st.Artists, &st.Albums)
	if err != nil {
		return st, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT format, COUNT(*) FROM tracks GROUP BY format ORDER BY COUNT(*) DESC, format`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var f FormatCount
		if err := rows.Scan(&f.Format, &f.Count); err != nil {
			return st, err
		}
		st.Formats = append(st.Formats, f)
	}
	return st, rows.Err()
}
