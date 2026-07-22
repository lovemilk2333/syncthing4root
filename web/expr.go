package main

import (
	"fmt"
	"strings"
)

// Boolean expression DSL for network-policy conditions.
//
// Grammar (precedence: NOT > AND > OR):
//
//	expr    := orExpr
//	orExpr  := andExpr (OR andExpr)*
//	andExpr := notExpr (AND notExpr)*
//	notExpr := NOT notExpr | atom
//	atom    := IDENT | '(' expr ')'
//
// Keywords AND/OR/NOT are case-insensitive; &&/||/! are accepted as synonyms.
// An empty expression is valid and evaluates to true (no constraint).

// ── tokens ──────────────────────────────────────────────────────────────────

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tAnd
	tOr
	tNot
	tLParen
	tRParen
)

type token struct {
	kind tokKind
	text string // for tIdent
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_' || b == '-'
}

// tokenize converts the source into tokens.
func tokenize(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen})
			i++
		case c == '!':
			toks = append(toks, token{kind: tNot})
			i++
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				i++
			}
			toks = append(toks, token{kind: tAnd})
			i++
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				i++
			}
			toks = append(toks, token{kind: tOr})
			i++
		case isIdentByte(c):
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			word := s[i:j]
			switch strings.ToUpper(word) {
			case "AND":
				toks = append(toks, token{kind: tAnd})
			case "OR":
				toks = append(toks, token{kind: tOr})
			case "NOT":
				toks = append(toks, token{kind: tNot})
			default:
				toks = append(toks, token{kind: tIdent, text: word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	toks = append(toks, token{kind: tEOF})
	return toks, nil
}

// ── AST ─────────────────────────────────────────────────────────────────────

type exprNode interface {
	eval(vals map[string]bool) bool
	idents(set map[string]bool)
}

type identNode struct{ name string }
type notNode struct{ x exprNode }
type andNode struct{ l, r exprNode }
type orNode struct{ l, r exprNode }
type trueNode struct{}

func (n identNode) eval(v map[string]bool) bool { return v[n.name] }
func (n notNode) eval(v map[string]bool) bool   { return !n.x.eval(v) }
func (n andNode) eval(v map[string]bool) bool   { return n.l.eval(v) && n.r.eval(v) }
func (n orNode) eval(v map[string]bool) bool    { return n.l.eval(v) || n.r.eval(v) }
func (trueNode) eval(map[string]bool) bool      { return true }

func (n identNode) idents(s map[string]bool) { s[n.name] = true }
func (n notNode) idents(s map[string]bool)   { n.x.idents(s) }
func (n andNode) idents(s map[string]bool)   { n.l.idents(s); n.r.idents(s) }
func (n orNode) idents(s map[string]bool)    { n.l.idents(s); n.r.idents(s) }
func (trueNode) idents(map[string]bool)      {}

// ── parser (recursive descent) ────────────────────────────────────────────

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) parseExpr() (exprNode, error) { return p.parseOr() }

func (p *parser) parseOr() (exprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orNode{left, right}
	}
	return left, nil
}

func (p *parser) parseAnd() (exprNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tAnd {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andNode{left, right}
	}
	return left, nil
}

func (p *parser) parseNot() (exprNode, error) {
	if p.peek().kind == tNot {
		p.next()
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notNode{x}, nil
	}
	return p.parseAtom()
}

func (p *parser) parseAtom() (exprNode, error) {
	t := p.next()
	switch t.kind {
	case tIdent:
		return identNode{t.text}, nil
	case tLParen:
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		p.next()
		return inner, nil
	case tEOF:
		return nil, fmt.Errorf("unexpected end of expression")
	default:
		return nil, fmt.Errorf("unexpected token")
	}
}

// ── public API ────────────────────────────────────────────────────────────

// parseExpr parses s into an AST. An empty/whitespace-only string yields a
// node that always evaluates to true (no constraint).
func parseExpr(s string) (exprNode, error) {
	if strings.TrimSpace(s) == "" {
		return trueNode{}, nil
	}
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing tokens")
	}
	return node, nil
}

// validateExpr checks syntax and that every identifier is in known.
func validateExpr(s string, known []string) error {
	node, err := parseExpr(s)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	node.idents(set)
	knownSet := map[string]bool{}
	for _, k := range known {
		knownSet[k] = true
	}
	for id := range set {
		if !knownSet[id] {
			return fmt.Errorf("unknown condition %q", id)
		}
	}
	return nil
}
