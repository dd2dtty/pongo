package template

/* Examples:
 * {% if a.test == b.bla %}
 * {{ name|capitalize }}
 * {{ date|fmt:"H:m d.m.Y" }}
 * {% if name|lower == "florian" %}
 * {# My comment
 *    goes over several lines 
 * #}
 */

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
)

const (
	nContent = iota
	nFilter
	nTag
)

type contentNode struct {
	line    int
	col     int
	content string
}

type filterNode struct {
	line    int
	col     int
	content string
	e       *expr
}

type tagNode struct {
	line    int
	col     int
	content string

	tagname    string
	tagargs    string
	taghandler *TagHandler

	ident string   // tag identifier, like 'if'
	args  []string // string list of arguments
}

type node interface {
	// A node must implement a execute() function which gets called when the template is executed
	execute(*Template, *Context) (*string, error)
	getLine() int
	getCol() int
	getContent() *string
}

type templateLocator func(*string) (*string, error)

type Template struct {
	name string // e.g. the filename, used for error messages

	// Parsing stuff
	parsed bool
	raw    string
	rawLen int

	pos    int
	start  int
	length int

	// Error handling
	err  string // contains nothing if there was no (parsing|executing) error
	line int
	col  int

	// Execution
	autosafe         bool
	nodes            []node
	node_pos         int
	locator          templateLocator
	internal_context Context
}

type stateFunc func(*Template) stateFunc

func processComment(tpl *Template) stateFunc {
	c, success := tpl.getChar(0)
	if !success {
		tpl.err = "File end reached within comment"
		return nil
	}

	if c == '#' {
		// Check next char for }
		nc, success := tpl.getChar(1) // curr + 1
		if !success {
			tpl.err = "File end reached within comment"
			return nil
		}
		if nc == '}' {
			tpl.fastForward(2)
			tpl.start = tpl.pos // Skip whole comment, start after comment
			return processContent
		}
	}

	tpl.fastForward(1)

	return processComment
}

func processFilter(tpl *Template) stateFunc {
	c, success := tpl.getChar(0)
	if !success {
		tpl.err = "File end reached within filter"
		return nil
	}

	if c == '}' {
		// Check next char for }
		nc, success := tpl.getChar(1) // curr + 1
		if !success {
			tpl.err = "File end reached within filter"
			return nil
		}
		if nc == '}' {
			// Add new filter node
			err := addFilterNode(tpl)
			if err != nil {
				tpl.err = err.Error()
				return nil
			}

			// Go back to content
			tpl.fastForward(2) // Ignore }}
			tpl.start = tpl.pos
			return processContent
		}
	}

	tpl.length++
	tpl.fastForward(1)

	return processFilter
}

func processTag(tpl *Template) stateFunc {
	c, success := tpl.getChar(0)
	if !success {
		tpl.err = "File end reached within tag"
		return nil
	}

	if c == '%' {
		// Check next char for }
		nc, success := tpl.getChar(1) // curr + 1
		if !success {
			tpl.err = "File end reached within tag"
			return nil
		}
		if nc == '}' {
			// Add new filter node
			err := addTagNode(tpl)
			if err != nil {
				tpl.err = err.Error()
				return nil
			}

			// Go back to content
			tpl.fastForward(2) // Ignore }}
			tpl.start = tpl.pos
			return processContent
		}
	}

	tpl.length++
	tpl.fastForward(1)

	return processTag
}

func processContent(tpl *Template) stateFunc {
	// Check if we reached the end
	c, success := tpl.getChar(0)
	if !success {
		addContentNode(tpl)
		return nil
	}

	if c == '{' {
		// Get next char
		nc, success := tpl.getChar(1)
		if !success {
			tpl.err = "File end reached (after opening '{')"
			return nil
		}

		tpl.fastForward(2) // skip next 2 chars {x

		switch nc {
		case '#':
			addContentNode(tpl)
			tpl.start = tpl.pos // y??????
			return processComment
		case '%':
			addContentNode(tpl)
			tpl.start = tpl.pos // y??????
			return processTag
		case '{':
			addContentNode(tpl)
			tpl.start = tpl.pos // y??????
			return processFilter
		default:
			tpl.err = fmt.Sprintf("Unknown open command ('%c').", nc)
			return nil
		}
	}

	tpl.length++
	tpl.fastForward(1)

	return processContent
}

func addContentNode(tpl *Template) {
	if tpl.length == 0 {
		return
	}

	cn := &contentNode{
		line:    tpl.line,
		col:     tpl.col,
		content: tpl.raw[tpl.start : tpl.start+tpl.length],
	}
	tpl.start = tpl.pos
	tpl.length = 0
	tpl.nodes = append(tpl.nodes, cn)
}

func (cn *contentNode) getCol() int         { return cn.col }
func (cn *contentNode) getLine() int        { return cn.line }
func (cn *contentNode) getContent() *string { return &cn.content }

func (cn *contentNode) execute(tpl *Template, ctx *Context) (*string, error) {

	return &cn.content, nil
}

func addFilterNode(tpl *Template) error {
	if tpl.length == 0 {
		return errors.New("Empty filter")
	}

	fn := &filterNode{
		line:    tpl.line,
		col:     tpl.col,
		content: strings.TrimSpace(tpl.raw[tpl.start : tpl.start+tpl.length]),
	}

	e, err := newExpr(&fn.content)
	if err != nil {
		return err
	}

	// Add 'safe' filter to those filter calls to make them
	// safe
	if tpl.autosafe {
		e.addFilter("safe")
	}

	fn.e = e

	tpl.start = tpl.pos
	tpl.length = 0
	tpl.nodes = append(tpl.nodes, fn)

	return nil
}

func (fn *filterNode) getCol() int         { return fn.col }
func (fn *filterNode) getLine() int        { return fn.line }
func (fn *filterNode) getContent() *string { return &fn.content }

func (fn *filterNode) execute(tpl *Template, ctx *Context) (*string, error) {
	//fmt.Printf("<filter '%s' expr=%s>\n", fn.content, fn.e)
	out, err := fn.e.evalString(ctx)
	/*if err != nil {
		return "", err, 0
	}*/
	//return out, nil, 1
	return out, err
}

func addTagNode(tpl *Template) error {
	if tpl.length == 0 {
		return errors.New("Empty tag")
	}

	tn := &tagNode{
		line:    tpl.line,
		col:     tpl.col,
		content: strings.TrimSpace(tpl.raw[tpl.start : tpl.start+tpl.length]),
	}

	// Split tagname from tagargs; example: <if> <name|lower == "florian">
	args := strings.SplitN(tn.content, " ", 2)
	if len(args) < 1 {
		return errors.New("Tag must contain at least a name")
	}
	tagname := args[0]
	var tagargs string
	if len(args) == 2 {
		tagargs = args[1]
	}

	tag, has_tag := Tags[tagname]
	if !has_tag {
		return errors.New(fmt.Sprintf("Tag '%s' does not exist", tagname))
	}

	tn.tagname = tagname
	tn.tagargs = strings.TrimSpace(tagargs)
	tn.taghandler = tag

	tpl.start = tpl.pos
	tpl.length = 0
	tpl.nodes = append(tpl.nodes, tn)
	return nil
}

func (tn *tagNode) getCol() int         { return tn.col }
func (tn *tagNode) getLine() int        { return tn.line }
func (tn *tagNode) getContent() *string { return &tn.content }

func (tn *tagNode) execute(tpl *Template, ctx *Context) (*string, error) {
	// Split tag from args and call it
	// Examples:
	// - If-clause: if name|lower == "florian"
	// - For-clause: for friend in person.friends
	// in general: <tagname> <payload>

	if tn.taghandler == nil {
		// We reached an unhandled placeholder (maybe 'else' of 'endif' for the if-clause)
		return nil, errors.New(fmt.Sprintf("Unhandled placeholder (for example 'endif' for an if-clause): '%s'", tn.tagname))
	}

	out, err := tn.taghandler.Execute(&tn.tagargs, tpl, ctx)
	return out, err
	//return fmt.Sprintf("<tag='%s'>", tn.content), nil, 1
}

// Reads a template from file. If there's no templateLocator provided, 
// one will be created to search for files in the same directory the template
// file is located. file_path can either be an absolute filepath or a relative one.
func FromFile(file_path string, locator templateLocator) (*Template, error) {
	var err error

	// What is file_path?
	if !filepath.IsAbs(file_path) {
		file_path, err = filepath.Abs(file_path)
		if err != nil {
			return nil, err
		}
	}

	buf, err := ioutil.ReadFile(file_path)
	if err != nil {
		return nil, err
	}

	file_base := filepath.Dir(file_path)

	if locator == nil {
		// Create a default locator
		locator = func(name *string) (*string, error) {
			filename := *name
			if !filepath.IsAbs(filename) {
				filename = filepath.Join(file_base, filename)
			}

			buf, err := ioutil.ReadFile(filename)
			if err != nil {
				return nil, errors.New(fmt.Sprintf("Could not find the template '%s' (default file locator): %v", filename, err))
			}

			bufstr := string(buf)
			return &bufstr, nil
		}
	}

	// Get file name from filepath
	name := filepath.Base(file_path)

	strbuf := string(buf)
	tpl, err := newTemplate(name, &strbuf, locator)
	if err != nil {
		return nil, err
	}

	err = tpl.parse()
	if err != nil {
		return nil, err
	}

	return tpl, nil
}

// Creates a new template instance from string.
func FromString(name string, tplstr *string, locator templateLocator) (*Template, error) {
	tpl, err := newTemplate(name, tplstr, locator)
	if err != nil {
		return nil, err
	}

	err = tpl.parse()
	if err != nil {
		return nil, err
	}

	return tpl, nil
}

func newTemplate(name string, tplstr *string, locator templateLocator) (*Template, error) {
	tplLen := len(*tplstr)

	if tplLen == 0 {
		return nil, errors.New("Template has no content")
	}

	tpl := &Template{
		name:             name,
		raw:              *tplstr,
		line:             1,
		rawLen:           tplLen,
		nodes:            make([]node, 0, 250),
		autosafe:         true,
		locator:          locator,
		internal_context: make(Context),
	}

	return tpl, nil
}

func (tpl *Template) parse() error {
	if tpl.parsed { // Already parsed?
		return nil
	}

	// Check pos=0 charachter (maybe it's a newline!)
	tpl.updatePosition()

	state := processContent(tpl)
	for state != nil {
		state = state(tpl)
	}

	if len(tpl.err) > 0 { // Parsing error occurred?
		return errors.New(fmt.Sprintf("[Parsing error: %s] [Line %d, Column %d] %s", tpl.name, tpl.line, tpl.col, tpl.err))
	}

	tpl.parsed = true

	return nil
}

func (tpl *Template) setInternalContext(ctx *Context) {
	tpl.internal_context = *ctx
}

func (tpl *Template) Execute(ctx *Context) (*string, error) {
	// fmt.Printf("[Template] Node items = %d\n", len(tpl.nodes))

	if *ctx == nil {
		ctx = &Context{}
	}

	renderedStrings := make([]string, 0, len(tpl.nodes))

	// TODO: We could replace this code by executeUntilAnyTagNode(ctx), but
	// it then includes some more interface checks which could hurt performance.
	// Not sure about this.

	for tpl.node_pos < len(tpl.nodes) {
		node := tpl.nodes[tpl.node_pos]
		str, err := node.execute(tpl, ctx)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("[Error: %s] [Line %d Col %d (%s)] %s", tpl.name, node.getLine(), node.getCol(), *node.getContent(), err))
		}
		renderedStrings = append(renderedStrings, *str)

		tpl.node_pos++
	}

	outputString := strings.Join(renderedStrings, "")

	return &outputString, nil
}

func (tpl *Template) executeUntilAnyTagNode(ctx *Context, nodenames ...string) (*tagNode, *[]string, error) {
	renderedStrings := make([]string, 0, len(tpl.nodes)-tpl.node_pos)

	// To avoid recursion, we first increase tpl.node_pos by one
	// (because the current node pos might point to the tag which calls executeUntilAnyTagNode)
	tpl.node_pos++

	for tpl.node_pos < len(tpl.nodes) {
		node := tpl.nodes[tpl.node_pos]
		if tn, is_tag := node.(*tagNode); is_tag {
			for _, name := range nodenames {
				if tn.tagname == name {
					// We have found one of the end-nodes, so generate the template result string and return it to
					// the caller
					return tn, &renderedStrings, nil
				}
			}
		}
		str, err := node.execute(tpl, ctx)
		if err != nil {
			return nil, nil, errors.New(fmt.Sprintf("[Error in block-execution: %s] [Line %d Col %d (%s)] %s", tpl.name, node.getLine(), node.getCol(), *node.getContent(), err))
		}
		renderedStrings = append(renderedStrings, *str)
		tpl.node_pos++
	}

	// One nodename MUST be executed! Otherwise error.
	return nil, nil, errors.New(fmt.Sprintf("No end-node (possible nodes: %v) found.", nodenames))
}

func (tpl *Template) ignoreUntilAnyTagNode(nodenames ...string) (*tagNode, error) {
	// To avoid recursion, we first increase tpl.node_pos by one
	// (because the current node pos might point to the tag which calls executeUntilAnyTagNode)
	tpl.node_pos++

	for tpl.node_pos < len(tpl.nodes) {
		node := tpl.nodes[tpl.node_pos]
		if tn, is_tag := node.(*tagNode); is_tag {
			for _, name := range nodenames {
				if tn.tagname == name {
					// We have found one of the end-nodes, so return now
					return tn, nil
				}
			}
			// Is not in nodenames, so ignore the tag!
			if tn.taghandler != nil && tn.taghandler.Ignore != nil {
				tn.taghandler.Ignore(&tn.tagargs, tpl)
			}
		}
		tpl.node_pos++
	}

	// One nodename MUST be executed! Otherwise error.
	return nil, errors.New(fmt.Sprintf("No end-node (possible nodes: %v) found.", nodenames))
}

func (tpl *Template) getChar(rel int) (byte, bool) {
	if tpl.hasReachedEnd(rel) {
		return 0, false
	}

	return tpl.raw[tpl.pos+rel], true
}

func (tpl *Template) hasReachedEnd(rel int) bool {
	if tpl.pos+rel >= tpl.rawLen {
		return true
	}
	return false
}

func (tpl *Template) fastForward(rel int) bool {
	for x := 0; x < rel; x++ {
		tpl.pos++
		if !tpl.updatePosition() {
			return false
		}
	}

	return true
}

// Must be called after every change of pos
func (tpl *Template) updatePosition() bool {
	if tpl.hasReachedEnd(0) {
		return false
	}

	if tpl.raw[tpl.pos] == '\n' {
		tpl.line++
		tpl.col = 0
	} else {
		tpl.col++
	}
	return true
}
