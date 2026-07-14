import { cx } from '../../lib/cx'
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
    <ul className={cx('m-0 list-none p-0 text-[0.85rem]', depth > 0 && 'pl-3')}>
      {nodes.map((n) => (
        <li key={n.path}>
          {n.isFile ? (
            <button
              type="button"
              className={cx(
                'block w-full cursor-pointer rounded border-0 bg-transparent px-1 py-0.5 text-left font-[inherit] text-fg hover:bg-accent/10 hover:text-accent',
                selected === n.path && 'bg-accent/10 text-accent',
              )}
              onClick={() => onSelect(n.path)}
            >
              {n.name}
            </button>
          ) : (
            <details open={depth < 1}>
              <summary className="cursor-pointer font-semibold text-muted">{n.name}/</summary>
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
