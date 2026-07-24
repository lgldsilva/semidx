package graph

import (
	"path"
	"sort"
	"strings"
)

// Default budgets — callers may lower them; zero means “use default”.
const (
	DefaultPathMaxDepth  = 8
	DefaultSubgraphDepth = 2
	DefaultMaxVisitNodes = 10_000
	DefaultMaxEdgesOut   = 500
)

// Node kinds in the walkable graph.
const (
	KindFile    = "file"
	KindPackage = "package"
)

// Edge kinds.
const (
	EdgeImports  = "imports"  // file → package (stored)
	EdgeContains = "contains" // package → file (synthetic)
)

// Budget limits expansion. Zero fields fall back to package defaults.
type Budget struct {
	MaxDepth      int // max BFS depth (hops)
	MaxVisitNodes int // stop after visiting this many nodes
	MaxEdgesOut   int // cap edges returned in a subgraph
}

func (b Budget) withPathDefaults() Budget {
	if b.MaxDepth <= 0 {
		b.MaxDepth = DefaultPathMaxDepth
	}
	if b.MaxVisitNodes <= 0 {
		b.MaxVisitNodes = DefaultMaxVisitNodes
	}
	if b.MaxEdgesOut <= 0 {
		b.MaxEdgesOut = DefaultMaxEdgesOut
	}
	return b
}

func (b Budget) withSubgraphDefaults() Budget {
	if b.MaxDepth <= 0 {
		b.MaxDepth = DefaultSubgraphDepth
	}
	if b.MaxVisitNodes <= 0 {
		b.MaxVisitNodes = DefaultMaxVisitNodes
	}
	if b.MaxEdgesOut <= 0 {
		b.MaxEdgesOut = DefaultMaxEdgesOut
	}
	return b
}

// Normalize returns a slash-separated project-relative path.
// A trailing slash on input is preserved (package-dir form).
func Normalize(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || p == "." {
		return ""
	}
	keepSlash := strings.HasSuffix(p, "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	if keepSlash && p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// PackageDir returns the package-directory form of a file path (trailing /).
// If p already looks like a package dir (ends with /), it is normalized and returned.
func PackageDir(p string) string {
	p = Normalize(p)
	if p == "" {
		return ""
	}
	if strings.HasSuffix(p, "/") {
		return p
	}
	// Heuristic: no extension and no dotted last segment → treat as package dir.
	base := path.Base(p)
	if !strings.Contains(base, ".") {
		if !strings.HasSuffix(p, "/") {
			return p + "/"
		}
		return p
	}
	dir := path.Dir(p)
	if dir == "." || dir == "" {
		return ""
	}
	return dir + "/"
}

// IsPackageDir reports whether p is in package-dir form (trailing slash) or
// an empty root package.
func IsPackageDir(p string) bool {
	p = Normalize(p)
	return p == "" || strings.HasSuffix(p, "/")
}

// Node is one vertex in a subgraph response.
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // file | package
	Seed  bool   `json:"seed,omitempty"`
}

// Edge is a directed relation (may have been traversed in reverse when undirected).
type Edge struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	Kind    string `json:"kind"`              // imports | contains
	Reverse bool   `json:"reverse,omitempty"` // true if walked against stored direction
}

// PathResult is the outcome of ShortestPath.
type PathResult struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Found     bool     `json:"found"`
	Directed  bool     `json:"directed"`
	Hops      []string `json:"hops,omitempty"`
	Edges     []Edge   `json:"edges,omitempty"`
	Length    int      `json:"length"`
	Truncated bool     `json:"truncated,omitempty"`
}

// SubgraphResult is an ego (or hub-sampled) neighborhood.
type SubgraphResult struct {
	Nodes     []Node `json:"nodes"`
	Edges     []Edge `json:"edges"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Index is an in-memory walkable view of FetchGraphNeighbors (+ optional file list).
type Index struct {
	// out[file] = package dirs (and occasional file targets) it imports
	out map[string][]string
	// filesInPkg[packageDir] = files in that directory
	filesInPkg map[string][]string
	// allFiles known
	files []string
}

// Build constructs an Index from the store adjacency map.
// neighbors: source_file → targets (usually package dirs with trailing /).
// files: optional full inventory; when empty, source keys of neighbors are used
// (files with zero outbound imports are then invisible for package→file hops).
func Build(neighbors map[string][]string, files []string) *Index {
	idx := &Index{
		out:        make(map[string][]string, len(neighbors)),
		filesInPkg: make(map[string][]string),
	}
	seenFile := map[string]struct{}{}
	addFile := func(f string) {
		f = Normalize(f)
		if f == "" || IsPackageDir(f) {
			return
		}
		if _, ok := seenFile[f]; ok {
			return
		}
		seenFile[f] = struct{}{}
		idx.files = append(idx.files, f)
		pkg := PackageDir(f)
		if pkg != "" {
			idx.filesInPkg[pkg] = append(idx.filesInPkg[pkg], f)
		}
	}

	for src, targets := range neighbors {
		s := Normalize(src)
		if s == "" {
			continue
		}
		addFile(s)
		uniq := make([]string, 0, len(targets))
		seenT := map[string]struct{}{}
		for _, t := range targets {
			t = Normalize(t)
			if t == "" {
				continue
			}
			// Prefer package-dir form for directory-like targets.
			if !strings.Contains(path.Base(t), ".") && !strings.HasSuffix(t, "/") {
				t += "/"
			}
			if _, ok := seenT[t]; ok {
				continue
			}
			seenT[t] = struct{}{}
			uniq = append(uniq, t)
		}
		sort.Strings(uniq)
		idx.out[s] = uniq
	}
	for _, f := range files {
		addFile(f)
	}
	sort.Strings(idx.files)
	for pkg, list := range idx.filesInPkg {
		sort.Strings(list)
		idx.filesInPkg[pkg] = list
	}
	return idx
}

// neighborsDirected returns outgoing walk edges from node (file or package).
func (idx *Index) neighborsDirected(node string) []Edge {
	node = Normalize(node)
	if node == "" {
		return nil
	}
	var out []Edge
	if IsPackageDir(node) {
		pkg := node
		if pkg != "" && !strings.HasSuffix(pkg, "/") {
			pkg += "/"
		}
		for _, f := range idx.filesInPkg[pkg] {
			out = append(out, Edge{Source: pkg, Target: f, Kind: EdgeContains})
		}
		return out
	}
	for _, t := range idx.out[node] {
		out = append(out, Edge{Source: node, Target: t, Kind: EdgeImports})
	}
	return out
}

// neighborsUndirected adds reverse imports/contains.
func (idx *Index) neighborsUndirected(node string) []Edge {
	node = Normalize(node)
	fwd := idx.neighborsDirected(node)
	var rev []Edge
	if IsPackageDir(node) {
		// Reverse of file→package imports: who imports this package?
		pkg := node
		if pkg != "" && !strings.HasSuffix(pkg, "/") {
			pkg += "/"
		}
		for src, targets := range idx.out {
			for _, t := range targets {
				if packageEqual(t, pkg) {
					rev = append(rev, Edge{Source: pkg, Target: src, Kind: EdgeImports, Reverse: true})
					break
				}
			}
		}
	} else {
		// Reverse of package→file contains: file → its package
		pkg := PackageDir(node)
		if pkg != "" {
			rev = append(rev, Edge{Source: node, Target: pkg, Kind: EdgeContains, Reverse: true})
		}
	}
	return append(fwd, rev...)
}

func packageEqual(a, b string) bool {
	a, b = Normalize(a), Normalize(b)
	if !strings.HasSuffix(a, "/") && a != "" {
		a += "/"
	}
	if !strings.HasSuffix(b, "/") && b != "" {
		b += "/"
	}
	return a == b
}

type bfsParent struct {
	prev string
	edge Edge
}

// ShortestPath finds a shortest hop path from → to.
// If allowUndirected is false, only directed dependency flow is used.
// If directed search fails and allowUndirected is true, retries undirected and
// sets Directed=false on success.
func (idx *Index) ShortestPath(from, to string, budget Budget, allowUndirected bool) PathResult {
	budget = budget.withPathDefaults()
	from, to = Normalize(from), Normalize(to)
	res := PathResult{From: from, To: to, Directed: true}
	if from == "" || to == "" {
		return res
	}
	if from == to {
		res.Found = true
		res.Hops = []string{from}
		return res
	}

	hops, edges, trunc, ok := idx.bfs(from, to, budget, false)
	if ok {
		res.Found = true
		res.Hops = hops
		res.Edges = edges
		res.Length = len(edges)
		res.Truncated = trunc
		return res
	}
	if trunc {
		res.Truncated = true
	}
	if !allowUndirected {
		return res
	}
	hops, edges, trunc2, ok := idx.bfs(from, to, budget, true)
	res.Truncated = res.Truncated || trunc2
	if ok {
		res.Found = true
		res.Directed = false
		res.Hops = hops
		res.Edges = edges
		res.Length = len(edges)
	}
	return res
}

func (idx *Index) bfs(from, to string, budget Budget, undirected bool) (hops []string, edges []Edge, truncated, found bool) {
	type item struct {
		node  string
		depth int
	}
	q := []item{{from, 0}}
	parent := map[string]bfsParent{}
	seen := map[string]struct{}{from: {}}
	visited := 0

	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		visited++
		if visited > budget.MaxVisitNodes {
			return nil, nil, true, false
		}
		if cur.depth >= budget.MaxDepth {
			continue
		}
		var nbrs []Edge
		if undirected {
			nbrs = idx.neighborsUndirected(cur.node)
		} else {
			nbrs = idx.neighborsDirected(cur.node)
		}
		// Stable order
		sort.Slice(nbrs, func(i, j int) bool {
			if nbrs[i].Target != nbrs[j].Target {
				return nbrs[i].Target < nbrs[j].Target
			}
			return nbrs[i].Kind < nbrs[j].Kind
		})
		for _, e := range nbrs {
			next := Normalize(e.Target)
			if next == "" {
				continue
			}
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			parent[next] = bfsParent{prev: cur.node, edge: e}
			if next == to {
				return reconstruct(from, to, parent)
			}
			q = append(q, item{next, cur.depth + 1})
		}
	}
	return nil, nil, false, false
}

func reconstruct(from, to string, parent map[string]bfsParent) ([]string, []Edge, bool, bool) {
	var hopsRev []string
	var edgesRev []Edge
	cur := to
	for cur != from {
		p, ok := parent[cur]
		if !ok {
			return nil, nil, false, false
		}
		hopsRev = append(hopsRev, cur)
		edgesRev = append(edgesRev, p.edge)
		cur = p.prev
	}
	hopsRev = append(hopsRev, from)
	// reverse
	hops := make([]string, len(hopsRev))
	for i := range hopsRev {
		hops[i] = hopsRev[len(hopsRev)-1-i]
	}
	edges := make([]Edge, len(edgesRev))
	for i := range edgesRev {
		edges[i] = edgesRev[len(edgesRev)-1-i]
	}
	return hops, edges, false, true
}

// Subgraph returns the neighborhood around seed up to budget.MaxDepth.
// If seed is empty, seeds are the highest out-degree files (hub sample).
func (idx *Index) Subgraph(seed string, budget Budget) SubgraphResult {
	budget = budget.withSubgraphDefaults()
	seed = Normalize(seed)
	res := SubgraphResult{}

	var seeds []string
	if seed != "" {
		seeds = []string{seed}
	} else {
		seeds = idx.hubSeeds(16)
	}
	if len(seeds) == 0 {
		return res
	}

	type item struct {
		node  string
		depth int
	}
	q := make([]item, 0, len(seeds))
	seen := map[string]struct{}{}
	seedSet := map[string]struct{}{}
	for _, s := range seeds {
		seen[s] = struct{}{}
		seedSet[s] = struct{}{}
		q = append(q, item{s, 0})
	}

	nodeSet := map[string]struct{}{}
	var edges []Edge
	edgeSeen := map[string]struct{}{}
	visited := 0

	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		visited++
		if visited > budget.MaxVisitNodes {
			res.Truncated = true
			break
		}
		nodeSet[cur.node] = struct{}{}
		if cur.depth >= budget.MaxDepth {
			continue
		}
		nbrs := idx.neighborsDirected(cur.node)
		sort.Slice(nbrs, func(i, j int) bool { return nbrs[i].Target < nbrs[j].Target })
		for _, e := range nbrs {
			key := e.Source + "\x00" + e.Target + "\x00" + e.Kind
			if _, ok := edgeSeen[key]; !ok {
				edgeSeen[key] = struct{}{}
				if len(edges) >= budget.MaxEdgesOut {
					res.Truncated = true
				} else {
					edges = append(edges, e)
					nodeSet[e.Target] = struct{}{}
				}
			}
			t := Normalize(e.Target)
			if _, ok := seen[t]; ok {
				continue
			}
			if res.Truncated && len(edges) >= budget.MaxEdgesOut {
				continue
			}
			seen[t] = struct{}{}
			q = append(q, item{t, cur.depth + 1})
		}
	}

	nodes := make([]Node, 0, len(nodeSet))
	for id := range nodeSet {
		kind := KindFile
		if IsPackageDir(id) {
			kind = KindPackage
		}
		_, isSeed := seedSet[id]
		nodes = append(nodes, Node{
			ID:    id,
			Label: path.Base(strings.TrimSuffix(id, "/")),
			Kind:  kind,
			Seed:  isSeed,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})
	res.Nodes = nodes
	res.Edges = edges
	return res
}

func (idx *Index) hubSeeds(n int) []string {
	type deg struct {
		file string
		d    int
	}
	var list []deg
	for f, targets := range idx.out {
		list = append(list, deg{f, len(targets)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].d != list[j].d {
			return list[i].d > list[j].d
		}
		return list[i].file < list[j].file
	})
	if len(list) > n {
		list = list[:n]
	}
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.file
	}
	return out
}
