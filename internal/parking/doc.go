// Package parking holds wake expressions and the evaluator that reads them. An expression is
// stored data and the evaluator is pure: it reads only the expression, the facts it is given,
// and the time it is given, so a lane parked before an upgrade wakes after it (FR-PARK-7).
package parking
