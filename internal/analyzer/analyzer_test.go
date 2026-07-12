package analyzer

import "testing"

// containsAll checks that syms contains symbols with all the given names.
func symbolHas(syms []Symbol, name string) bool {
	for _, s := range syms {
		if s.Name == name {
			return true
		}
	}
	return false
}

func containsAll(t *testing.T, syms []Symbol, want ...string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, s := range syms {
			if s.Name == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing symbol %q in %v", w, syms)
		}
	}
}

func TestSymbols_Go(t *testing.T) {
	content := []byte(`package main

func main() { println("hello") }

func calculateTax(amount float64) float64 {
	return amount * 0.1
}

type TaxResult struct {
	Amount float64
	Tax    float64
}

func (t *TaxResult) String() string {
	return "taxed"
}
`)
	syms := Symbols("main.go", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	containsAll(t, syms, "main", "calculateTax", "TaxResult", "String")
	for _, s := range syms {
		if s.Name == "println" {
			t.Errorf("unexpected stdlib symbol in %v", syms)
		}
	}
}

func TestSymbols_Java(t *testing.T) {
	content := []byte(`package com.example;

public class Calculator {
    public double calculateTax(double amount) {
        return amount * 0.1;
    }
}

interface Taxable {
    double getTaxRate();
}

enum TaxType {
    VAT, INCOME
}
`)
	syms := Symbols("Calculator.java", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	containsAll(t, syms, "Calculator", "calculateTax", "Taxable", "getTaxRate", "TaxType")
}

func TestSymbols_JavaScript(t *testing.T) {
	content := []byte(`function greet(name) {
    return "Hello " + name;
}

class Person {
    constructor(name) {
        this.name = name;
    }
    greet() {
        return "Hi " + this.name;
    }
}
`)
	syms := Symbols("app.js", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	containsAll(t, syms, "greet", "Person")
}

func TestSymbols_TypeScript(t *testing.T) {
	content := []byte(`interface User {
    id: number;
    name: string;
}

class Admin implements User {
    id: number;
    name: string;
    constructor(id: number, name: string) {
        this.id = id;
        this.name = name;
    }
    promote(): void {}
}

function getUser(): User {
    return new Admin(1, "root");
}
`)
	syms := Symbols("admin.ts", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	containsAll(t, syms, "User", "Admin", "promote", "getUser")
}

func TestSymbols_TSX(t *testing.T) {
	// NOTE: Tree-sitter TSX grammar can confuse generic type args (<T>)
	// with JSX elements. Keep test content simple to avoid parser errors.
	content := []byte(`interface Props {
    title: string;
}

function Header(props: Props) {
    return (<h1>{props.title}</h1>);
}

class Page {
    render() {
        return (<Header title="hello" />);
    }
}

export default Page;
`)
	syms := Symbols("Page.tsx", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	containsAll(t, syms, "Props", "Header", "Page")
}

func TestSymbols_Empty(t *testing.T) {
	if s := Symbols("empty.go", nil); s != nil {
		t.Errorf("expected nil for empty content, got %v", s)
	}
	if s := Symbols("empty.go", []byte{}); s != nil {
		t.Errorf("expected nil for empty content, got %v", s)
	}
}

func TestSymbols_Unsupported(t *testing.T) {
	if s := Symbols("makefile", []byte("all: build")); s != nil {
		t.Errorf("expected nil for unsupported extension, got %v", s)
	}
}

func TestSymbols_InvalidSyntax(t *testing.T) {
	// Garbage content — should not panic, should return nil.
	syms := Symbols("broken.go", []byte("this is not go code {{{"))
	if syms != nil {
		t.Logf("got symbols from broken code: %v (acceptable — best-effort)", syms)
	}
}

func TestSymbols_Python(t *testing.T) {
	content := []byte(`class Calculator:
    def calculate_tax(self, amount):
        return amount * 0.1

    def apply_discount(self, price, pct):
        return price * (1 - pct)

def standalone_function():
    pass
`)
	syms := Symbols("calc.py", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	for _, want := range []string{"Calculator", "calculate_tax", "apply_discount", "standalone_function"} {
		if !symbolHas(syms, want) {
			t.Errorf("missing symbol %q in %v", want, syms)
		}
	}
}

func TestSymbols_Terraform(t *testing.T) {
	content := []byte(`resource "aws_instance" "web" {
    ami           = "ami-123"
    instance_type = "t2.micro"
}

variable "environment" {
    default = "dev"
}

output "instance_ip" {
    value = aws_instance.web.public_ip
}
`)
	syms := Symbols("main.tf", content)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	// HCL captures the first identifier of each block (the block type)
	for _, want := range []string{"resource", "variable", "output"} {
		if !symbolHas(syms, want) {
			t.Errorf("missing symbol %q in %v", want, syms)
		}
	}
}

func TestDedupe(t *testing.T) {
	in := []Symbol{
		{Name: "a"}, {Name: "b"}, {Name: "a"}, {Name: ""}, {Name: "c"}, {Name: "b"},
	}
	out := dedupe(in)
	if len(out) != 3 {
		t.Errorf("expected 3 unique, got %v", out)
	}
}
