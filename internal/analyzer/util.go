package analyzer

func dedupe(symbols []Symbol) []Symbol {
	if len(symbols) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(symbols))
	result := make([]Symbol, 0, len(symbols))
	for _, s := range symbols {
		if s.Name == "" {
			continue
		}
		if !seen[s.Name] {
			seen[s.Name] = true
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
