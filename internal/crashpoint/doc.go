// Package crashpoint names the exact steps where a crash test may kill a process.
// Without the chaos build tag, Hit is an empty function that the compiler removes,
// so a release binary holds no test switch.
package crashpoint
