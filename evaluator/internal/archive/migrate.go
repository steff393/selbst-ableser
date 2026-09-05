package archive

import "fmt"

// MigrationIssue names one thing that could not be imported, and why: a
// "meter X, day Y" description of the conflicting entry. An import must
// name a problem rather than silently skip it.
type MigrationIssue struct {
	File string
	Err  error
}

// MigrationReport summarizes one ImportFile run.
type MigrationReport struct {
	FilesProcessed   int
	EntriesInserted  int
	EntriesUnchanged int
	Issues           []MigrationIssue
}

// ImportFile imports every entry from the archive-schema SQLite database at
// sourcePath into dest, following the same conflict rules as
// Store.InsertHistorical: an entry already present with different values is
// recorded as an issue rather than overwritten, so importing the same file
// twice — or two files that overlap — is always safe. sourcePath is only
// ever read, never modified.
func ImportFile(dest *Store, sourcePath string) (MigrationReport, error) {
	var report MigrationReport

	source, err := OpenStore(sourcePath)
	if err != nil {
		return report, fmt.Errorf("opening %s: %w", sourcePath, err)
	}
	defer source.Close()

	entries, err := source.AllEntries()
	if err != nil {
		return report, fmt.Errorf("reading %s: %w", sourcePath, err)
	}
	report.FilesProcessed = 1

	for _, e := range entries {
		changed, ierr := dest.InsertHistorical(e)
		if ierr != nil {
			report.Issues = append(report.Issues, MigrationIssue{
				File: fmt.Sprintf("meter %s, day %s", e.MeterID, e.Day),
				Err:  ierr,
			})
			continue
		}
		if changed {
			report.EntriesInserted++
		} else {
			report.EntriesUnchanged++
		}
	}
	return report, nil
}
