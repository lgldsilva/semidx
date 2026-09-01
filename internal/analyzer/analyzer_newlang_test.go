package analyzer

import "testing"

func TestSymbols_Rust(t *testing.T) {
	content := []byte(`
pub fn greet() {}

pub struct Point {
    x: i32,
}

pub enum Color {
    Red,
}

impl Point {}
`)
	syms := Symbols("lib.rs", content)
	if len(syms) == 0 {
		t.Fatal("expected rust symbols, got none")
	}
	containsAll(t, syms, "greet", "Point", "Color")
}

func TestSymbols_C(t *testing.T) {
	content := []byte(`
struct Point {
    int x;
};

int add(int a, int b) {
    return a + b;
}
`)
	syms := Symbols("point.c", content)
	if len(syms) == 0 {
		t.Fatal("expected C symbols, got none")
	}
	containsAll(t, syms, "add", "Point")
}

func TestSymbols_Cpp(t *testing.T) {
	content := []byte(`
class Widget {};

struct Point {};

void draw() {}
`)
	syms := Symbols("widget.cpp", content)
	if len(syms) == 0 {
		t.Fatal("expected C++ symbols, got none")
	}
	containsAll(t, syms, "Widget", "Point", "draw")
}

func TestSymbols_CSharp(t *testing.T) {
	content := []byte(`
public class Calculator {
    public double Add(double amount) {
        return amount;
    }
}

interface ITaxable {
    double Rate();
}

enum TaxType {
    Vat
}
`)
	syms := Symbols("Calculator.cs", content)
	if len(syms) == 0 {
		t.Fatal("expected C# symbols, got none")
	}
	containsAll(t, syms, "Calculator", "Add", "ITaxable", "TaxType")
}

func TestSymbols_Ruby(t *testing.T) {
	content := []byte(`
module Tax
  class Calculator
    def calculate_tax
      0
    end
  end
end
`)
	syms := Symbols("tax.rb", content)
	if len(syms) == 0 {
		t.Fatal("expected Ruby symbols, got none")
	}
	containsAll(t, syms, "Tax", "Calculator", "calculate_tax")
}

func TestSymbols_PHP(t *testing.T) {
	content := []byte(`<?php
class Calculator {
    function add() {}
}
interface Taxable {}
function standalone() {}
`)
	syms := Symbols("calc.php", content)
	if len(syms) == 0 {
		t.Fatal("expected PHP symbols, got none")
	}
	containsAll(t, syms, "Calculator", "add", "Taxable", "standalone")
}

func TestSymbols_Swift(t *testing.T) {
	content := []byte(`
class Calculator {}
protocol Taxable {}
func add() {}
`)
	syms := Symbols("Calc.swift", content)
	if len(syms) == 0 {
		t.Fatal("expected Swift symbols, got none")
	}
	containsAll(t, syms, "Calculator", "Taxable", "add")
}

func TestSymbols_Bash(t *testing.T) {
	content := []byte(`
greet() {
  echo hello
}
`)
	syms := Symbols("greet.sh", content)
	if len(syms) == 0 {
		t.Fatal("expected bash symbols, got none")
	}
	containsAll(t, syms, "greet")
	if Symbols("greet.bash", content) == nil {
		t.Error(".bash extension should use the same extractor")
	}
}

func TestSymbols_HeaderAndCppAliases(t *testing.T) {
	c := []byte("struct Point { int x; };\nint add(int a, int b) { return a + b; }\n")
	if len(Symbols("point.h", c)) == 0 {
		t.Error(".h should reuse the C extractor")
	}
	cpp := []byte("class Widget {};\nvoid draw() {}\n")
	if len(Symbols("widget.cc", cpp)) == 0 {
		t.Error(".cc should reuse the C++ extractor")
	}
	if len(Symbols("widget.hpp", cpp)) == 0 {
		t.Error(".hpp should reuse the C++ extractor")
	}
}
