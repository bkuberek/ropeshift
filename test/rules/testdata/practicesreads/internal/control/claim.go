// Package control breaks the rule on purpose: a claim reads a repository's practices findings to decide.
package control

type practices interface {
	Result(repository string) (string, error)
}

type tx interface {
	Practices() practices
}

func mayClaim(t tx, repository string) bool {
	_, err := t.Practices().Result(repository)
	return err == nil
}
