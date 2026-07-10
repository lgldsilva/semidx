import type { TreeNode } from './buildFileTree'

export function TreeView({
  nodes,
  selected,
  onSelect,
  depth = 0,
}: {
  nodes: TreeNode[]
  selected: string
  onSelect: (path: string) => void
  depth?: number
}) {
  return (
    <ul className="tree" style={{ paddingLeft: depth ? 12 : 0 }}>
      {nodes.map((n) => (
        <li key={n.path}>
          {n.isFile ? (
            <button
              type="button"
              className={`tree-file ${selected === n.path ? 'active' : ''}`}
              onClick={() => onSelect(n.path)}
            >
              {n.name}
            </button>
          ) : (
            <details open={depth < 1}>
              <summary className="tree-dir">{n.name}/</summary>
              {n.children && (
                <TreeView
                  nodes={n.children}
                  selected={selected}
                  onSelect={onSelect}
                  depth={depth + 1}
                />
              )}
            </details>
          )}
        </li>
      ))}
    </ul>
  )
}
