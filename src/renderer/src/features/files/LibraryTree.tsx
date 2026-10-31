import {Button} from '@heroui/react'
import {ChevronRight, File, Files, Folder, FolderOpen, RefreshCw} from 'lucide-react'
import {useEffect, useRef, useState} from 'react'
import type {LibraryEntry} from '@shared/types'
import {isHiddenLibraryEntry, libraryFolderLabels} from '@shared/library-labels'

/** 工作目录树：按需读取子目录，避免一次性扫描整个资料库。 */
export function LibraryTree({
 selected,
 onSelect,
 onOpen,
 onRefresh,
 refreshKey
}: Readonly<{
 selected: string
 onSelect: (path: string) => void
 onOpen: (entry: LibraryEntry) => void
 onRefresh?: () => void
 refreshKey: number
}>) {
 const [root, setRoot] = useState<LibraryEntry[] | null>(null)
 const [children, setChildren] = useState<Record<string, LibraryEntry[]>>({})
 const [expanded, setExpanded] = useState<Set<string>>(new Set())
 const [loading, setLoading] = useState<Set<string>>(new Set())
 const [failed, setFailed] = useState<Set<string>>(new Set())
 const [error, setError] = useState('')
 const expandedRef = useRef(expanded)
 expandedRef.current = expanded

 async function loadDir(path: string) {
  setLoading(current => new Set(current).add(path))
  setFailed(current => {
   const next = new Set(current)
   next.delete(path)
   return next
  })
  try {
   const entries = await window.assistant.invoke<LibraryEntry[]>('library:dir', {path})
   if (path) setChildren(current => ({...current, [path]: entries}))
   else setRoot(entries)
   setError('')
   return entries
  } catch (reason) {
   setFailed(current => new Set(current).add(path))
   if (!path) setError(reason instanceof Error ? reason.message : String(reason))
   throw reason
  } finally {
   setLoading(current => {
    const next = new Set(current)
    next.delete(path)
    return next
   })
  }
 }

 async function reloadAll() {
  try {
   await loadDir('')
   const paths = [...expandedRef.current]
   const next: Record<string, LibraryEntry[]> = {}
   await Promise.all(
    paths.map(async path => {
     try {
      next[path] = await window.assistant.invoke<LibraryEntry[]>('library:dir', {path})
     } catch {
      /* 展开目录可能已被移动或删除，保留其余目录即可。 */
     }
    })
   )
   setChildren(next)
  } catch {
   /* 错误已显示在目录树顶部。 */
  }
 }

 // refreshKey 是显式刷新信号，展开集合通过 ref 读取。
 useEffect(() => {
  void reloadAll()
 }, [refreshKey])

 function activate(entry: LibraryEntry) {
  if (entry.type === 'file') {
   onOpen(entry)
   return
  }
  onSelect(entry.path)
  const open = expanded.has(entry.path)
  setExpanded(current => {
   const next = new Set(current)
   if (open) next.delete(entry.path)
   else next.add(entry.path)
   return next
  })
  if (!open && !children[entry.path]) void loadDir(entry.path).catch(() => {})
 }

 function renderEntries(entries: LibraryEntry[]) {
  return entries
   .filter(entry => !isHiddenLibraryEntry(entry))
   .map(entry => {
    const isDir = entry.type === 'dir'
    const isOpen = isDir && expanded.has(entry.path)
    const childEntries = children[entry.path]
    const visibleChildren = childEntries?.filter(item => !isHiddenLibraryEntry(item)) ?? null
    const label = isDir && !entry.path.includes('/') ? (libraryFolderLabels[entry.name] ?? entry.name) : entry.name
    return (
     <div className="library-tree__node" key={entry.path}>
      <button
       type="button"
       role="treeitem"
       aria-selected={selected === entry.path}
       aria-expanded={isDir ? isOpen : undefined}
       className={'library-tree__row' + (selected === entry.path ? ' is-selected' : '')}
       title={entry.path}
       onClick={() => activate(entry)}
      >
       {isDir ? (
        <ChevronRight size={14} className={'library-tree__chevron' + (isOpen ? ' is-open' : '')} aria-hidden="true" />
       ) : (
        <span className="library-tree__spacer" aria-hidden="true" />
       )}
       {isDir ? (
        isOpen ? (
         <FolderOpen size={15} aria-hidden="true" />
        ) : (
         <Folder size={15} aria-hidden="true" />
        )
       ) : (
        <File size={15} aria-hidden="true" />
       )}
       <span className="library-tree__name">{label}</span>
      </button>
      {isOpen && (
       <div className="library-tree__children" role="group">
        {visibleChildren?.length ? (
         renderEntries(childEntries ?? [])
        ) : loading.has(entry.path) ? (
         <span className="library-tree__hint">正在读取…</span>
        ) : failed.has(entry.path) ? (
         <button type="button" className="library-tree__retry" onClick={() => void loadDir(entry.path).catch(() => {})}>
          读取失败，点此重试
         </button>
        ) : (
         <span className="library-tree__hint">空目录</span>
        )}
       </div>
      )}
     </div>
    )
   })
 }

 const visibleRoot = root?.filter(entry => !isHiddenLibraryEntry(entry)) ?? null
 return (
  <div className="library-tree">
   <div className="library-tree__toolbar">
    <span>工作目录</span>
    <Button
     variant="ghost"
     size="sm"
     isIconOnly
     aria-label="刷新资料目录"
     isDisabled={loading.has('')}
     onPress={() => (onRefresh ? onRefresh() : void reloadAll())}
    >
     <RefreshCw size={14} aria-hidden="true" />
    </Button>
   </div>
   {error ? <p className="library-tree__error">{error}</p> : null}
   <div className="library-tree__nodes" role="tree" aria-label="资料目录">
    <button
     type="button"
     role="treeitem"
     aria-selected={selected === ''}
     className={'library-tree__row' + (selected === '' ? ' is-selected' : '')}
     onClick={() => onSelect('')}
    >
     <span className="library-tree__spacer" aria-hidden="true" />
     <Files size={15} aria-hidden="true" />
     <span className="library-tree__name">全部文件</span>
    </button>
    {root === null ? (
     <span className="library-tree__hint">正在读取目录…</span>
    ) : visibleRoot?.length ? (
     renderEntries(visibleRoot)
    ) : (
     <span className="library-tree__hint">资料目录为空</span>
    )}
   </div>
  </div>
 )
}
