package postgres

import "strings"

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func containsPattern(term string) string {
	return "%" + likeEscaper.Replace(term) + "%"
}
