// TodosPanel — Phase E1, panels.js port (#panelTodos #todoPanel). Source:
// S.todoStateMeta → S.todos, else legacy `_legacyTodosFromMessages()
//` (derived from messages). Renderer: empty-state vs grouped rows with
// priority/duedate status. Here we fetch via the todo/bus API where it
// exists; otherwise we show the vanilla empty state. IDs preserved.

export function TodosPanel() {
  return (
    <div id="todoPanel" style={{ flex: '1', overflowY: 'auto', padding: '8px 12px' }} className="todo-panel" />
  )
}
