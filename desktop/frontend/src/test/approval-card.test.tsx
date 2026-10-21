import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import "@/i18n"
import { ApprovalCard, fieldDiffText } from "@/components/chat/approval-card"
import type { ChatApproval } from "@/lib/backend-types"

afterEach(cleanup)

const base: ChatApproval = {
  convID: "c1",
  runID: "r1",
  approvalID: "a1",
  kind: "app",
  tool: "update_doc",
  site: "acme-prod",
  doctypes: ["ToDo"],
  names: ["TD-0001"],
  args: { doctype: "ToDo", name: "TD-0001", data: { status: "Closed" } },
  diff: [{ field: "status", old: "Open", new: "Closed" }],
  noChanges: false,
  docs: [],
  message: "",
}

describe("ApprovalCard", () => {
  it("shows the change, the target, the exact args and the diff", () => {
    const { container } = render(<ApprovalCard card={base} onAnswer={() => {}} />)
    expect(screen.getByText("The assistant wants to make a change")).toBeTruthy()
    expect(screen.getByText("acme-prod")).toBeTruthy()
    expect(screen.getByText("TD-0001")).toBeTruthy()
    const diff = screen.getByLabelText("Field changes")
    expect(diff.textContent).toContain("Open")
    expect(diff.textContent).toContain("Closed")
    // The request stays behind its disclosure when the table shows the change.
    expect(container.textContent).not.toContain('"status": "Closed"')
    fireEvent.click(screen.getByRole("button", { name: "Technical details" }))
    expect(container.textContent).toContain('"status": "Closed"')
  })

  it("says when there are no field changes", () => {
    render(<ApprovalCard card={{ ...base, diff: [], noChanges: true }} onAnswer={() => {}} />)
    expect(screen.getByText("No field changes")).toBeTruthy()
    expect(screen.queryByLabelText("Field changes")).toBeNull()
  })

  it("shows each document of a bulk_update", () => {
    const card: ChatApproval = {
      ...base,
      tool: "bulk_update",
      names: ["TD-0001", "TD-0002"],
      args: { doctype: "ToDo", data: [{ name: "TD-0001", status: "Closed" }, { name: "TD-0002", status: "Closed" }] },
      diff: [],
      docs: [
        { name: "TD-0001", diff: [{ field: "status", old: "Open", new: "Closed" }], noChanges: false },
        { name: "TD-0002", diff: [], noChanges: true },
      ],
    }
    const { container } = render(<ApprovalCard card={card} onAnswer={() => {}} />)
    expect(screen.getByLabelText("Field changes in TD-0001").textContent).toContain("Closed")
    expect(screen.queryByLabelText("Field changes in TD-0002")).toBeNull()
    expect(screen.getByText("No field changes")).toBeTruthy()
    expect(container.textContent).not.toContain('"status": "Closed"')
  })

  it("shows every field a copy sets, next to the original", () => {
    const card: ChatApproval = {
      ...base,
      tool: "copy_doc",
      args: { doctype: "ToDo", name: "TD-0001", data: { date: "2026-10-20" } },
      diff: [{ field: "date", old: "2026-10-12", new: "2026-10-20" }],
    }
    render(<ApprovalCard card={card} onAnswer={() => {}} />)
    const table = screen.getByLabelText("Fields the request sets")
    expect(table.textContent).toContain("In the original")
    expect(table.textContent).toContain("In the new document")
    expect(table.textContent).toContain("2026-10-20")
    cleanup()
    render(<ApprovalCard card={{ ...card, diff: [], noChanges: true }} onAnswer={() => {}} />)
    expect(screen.getByText("The request sets no field")).toBeTruthy()
    expect(document.body.textContent).toContain('"doctype": "ToDo"')
  })

  it("shows the ffc question for an ffc card", () => {
    const card = { ...base, kind: "ffc" as const, tool: "delete_doc", diff: [], message: "Delete ToDo TD-0001?" }
    render(<ApprovalCard card={card} onAnswer={() => {}} />)
    expect(screen.getByText("Delete ToDo TD-0001?")).toBeTruthy()
    expect(screen.queryByLabelText("Field changes")).toBeNull()
    // The exact request is open without a click, and Decline has the focus here too.
    expect(document.body.textContent).toContain('"doctype": "ToDo"')
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Decline" }))
  })

  it("focuses Decline and reports the answer", () => {
    const answers: boolean[] = []
    render(<ApprovalCard card={base} onAnswer={(a) => answers.push(a)} />)
    const decline = screen.getByRole("button", { name: "Decline" })
    expect(document.activeElement).toBe(decline)
    fireEvent.click(decline)
    fireEvent.click(screen.getByRole("button", { name: "Approve" }))
    expect(answers).toEqual([false, true])
  })

  it("shows each changed field with its value before and after", () => {
    const card = { ...base, diff: [{ field: "d", old: "---x", new: "+++y" }] }
    render(<ApprovalCard card={card} onAnswer={() => {}} />)
    const row = screen.getByRole("row", { name: /^d/ })
    const cells = row.querySelectorAll("td")
    expect(cells[0].textContent).toBe("---x")
    expect(cells[1].textContent).toBe("+++y")
  })

  it("writes every line of a multi-line value with its sign", () => {
    expect(fieldDiffText([{ field: "d", old: "a\nb", new: null }])).toBe("@@ d @@\n-a\n-b\n+null")
  })
})
