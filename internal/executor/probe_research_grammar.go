package executor

// buildResearchMarkdownGrammar constructs a raw GBNF grammar that enforces
// structural Markdown output containing a Title/Overview, Detailed Analysis,
// a structured Markdown Comparison Table, and a bulleted Sources & Citations list.
func buildResearchMarkdownGrammar(goal string) string {
	return `root ::= overview-section analysis-section table-section sources-section

overview-section ::= "# " [^\n]+ "\n\n" paragraph "\n\n"

analysis-section ::= ("## " [^\n]+ "\n\n" (paragraph | list-item)+ "\n\n")+

table-section ::= "## Comparative Overview\n\n" table-header table-divider table-row+ "\n"
table-header  ::= "| " ([^|\n]+ " | ")+ "\n"
table-divider ::= "| " ("--- | ")+ "\n"
table-row     ::= "| " ([^|\n]+ " | ")+ "\n"

sources-section ::= "## Sources & Citations\n\n" source-item+
source-item     ::= "- " [^\n]+ "\n"

paragraph ::= [^\n]+ ("\n" [^\n]+)*
list-item ::= "- " [^\n]+ "\n"
`
}
