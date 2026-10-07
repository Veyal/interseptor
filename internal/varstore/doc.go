// Package varstore resolves {{variable}} templates for collections. One pure
// resolver serves the UI preview, runner, CLI and MCP so there is no second
// implementation to drift.
//
// Precedence, narrow to wide: local (one request) > data (runner row) >
// environment > folder (inner to outer) > collection > global.
//
// Syntax: {{name}}, {{$dynamic}}, {{name|pipe|pipe:arg}}, nested names
// {{a{{b}}}}, and \{{ for a literal "{{". Values are themselves templates and
// expand recursively under depth, cycle, expansion and output limits.
// Unresolved variables block by default (PolicyBlock); Postman's silent
// "send {{x}} literally" is an explicit opt-in (PolicyLiteral).
package varstore
