package practicegit

// WithIgnoreCopyLimits returns snapshots that copy at most files `.gitignore` files and bytes bytes of them.
func WithIgnoreCopyLimits(s Snapshots, files int, bytes int64) Snapshots {
	s.ignoreLimits = ignoreCopyLimits{files: files, bytes: bytes}
	return s
}
