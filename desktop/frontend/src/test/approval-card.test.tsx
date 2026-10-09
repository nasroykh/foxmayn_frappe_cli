import { cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"

import "@/i18n"
import { ApprovalCard, fieldDiffText } from "@/components/chat/approval-card"
import type { ChatApproval } from "@/lib/backend-types"

afterEach(cleanup)

const base: ChatApproval = {
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
    expect(container.textContent).not.toContain('"status": "Closed"')
    fireEvent.click(screen.getByRole("button", { name: "Exact request" }))
    expect(container.textContent).toContain('"status": "Closed"')
  })

  it("says when there are no field changes", () => {
    render(<ApprovalCard card={{ ...base, diff: [], noChanges: true }} onAnswer={() => {}} />)
    expect(screen.getByText("No field changes")).toBeTruthy()
    expect(screen.queryByLabelText("Field changes")).toBeNull()
  })

  it("shows the ffc question for an ffc card", () => {
    const card = { ...base, kind: "ffc" as const, tool: "delete_doc", diff: [], message: "Delete ToDo TD-0001?" }
    render(<ApprovalCard card={card} onAnswer={() => {}} />)
    expect(screen.getByText("Delete ToDo TD-0001?")).toBeTruthy()
    expect(screen.queryByLabelText("Field changes")).toBeNull()
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

  it("writes every line of a multi-line value with its sign", () => {
    expect(fieldDiffText([{ field: "d", old: "a\nb", new: null }])).toBe("@@ d @@\n-a\n-b\n+null")
  })
})
