export type TreeNode = {
  name: string
  path: string
  children?: TreeNode[]
  isFile: boolean
}

/** Build a nested tree from flat path list. */
export function buildFileTree(paths: string[]): TreeNode[] {
  const root: TreeNode[] = []
  const dirMap = new Map<string, TreeNode>()

  const ensureDir = (parts: string[]): TreeNode[] => {
    let list = root
    let acc = ''
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part
      let node = dirMap.get(acc)
      if (!node) {
        node = { name: part, path: acc, children: [], isFile: false }
        dirMap.set(acc, node)
        list.push(node)
      }
      list = node.children!
    }
    return list
  }

  const sorted = [...paths].sort()
  for (const p of sorted) {
    const parts = p.split('/').filter(Boolean)
    if (parts.length === 0) continue
    const file = parts[parts.length - 1]
    const dirs = parts.slice(0, -1)
    const parent = dirs.length ? ensureDir(dirs) : root
    parent.push({ name: file, path: p, isFile: true })
  }

  const sortNodes = (nodes: TreeNode[]) => {
    nodes.sort((a, b) => {
      if (a.isFile !== b.isFile) return a.isFile ? 1 : -1
      return a.name.localeCompare(b.name)
    })
    for (const n of nodes) {
      if (n.children) sortNodes(n.children)
    }
  }
  sortNodes(root)
  return root
}
