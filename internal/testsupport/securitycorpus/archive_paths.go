package securitycorpus

// UnsafeArchivePaths returns path spellings that an archive reader must reject
// instead of normalizing into a different extraction target.
func UnsafeArchivePaths() []string {
	return []string{
		"../escape",
		"release/../../escape",
		"/absolute/path",
		"//server/share",
		`C:\escape`,
		`release\file`,
		`release\..\escape`,
		"release/./file",
		"release//file",
		"release/file\x00name",
		"release/NUL",
		"release/con.txt",
		"release/COM1.log",
		"release/file.txt:payload",
		"release/trailing.",
		"release/trailing ",
		"release/line\nbreak",
	}
}
