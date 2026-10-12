package store

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Backup writes a consistent copy of the database to dest (VACUUM INTO), safe
// while other processes read or write. dest must not exist.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup %s: file exists", dest)
	}
	// VACUUM INTO takes a string literal, not a parameter.
	lit := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO "+lit); err != nil {
		os.Remove(dest)
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}
