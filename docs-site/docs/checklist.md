# Role-scoped implementation checklist (AI-assisted, non-binding)

The checklist generator turns your in-scope controls into a per-team to-do list:
for each control it derives the owning team, then drafts the concrete tasks that
team must do to implement the control. It is an **AI-assisted draft** — review and
approve each team's section before you use it.

## What it does

1. **Assigns each control to a team — deterministically.** The split into
   `infrastructure`, `engineering`, and `security` is derived from each control's
   owner role (and, where that is blank, its applicability scope). This step does
   **not** use the AI model: it is a fixed, auditable mapping. A control whose owner
   role matches no team is surfaced under an honest **Unassigned** section so you
   can set its owner — it is never silently dropped.
2. **Drafts the tasks — with the local AI model.** For each team, the model turns
   that team's controls into one or more imperative to-do items. Every task cites
   the control it derives from, and may cite the control's SCF anchor and any linked
   policy.

## What it will not do (the guarantees)

- **No fabricated coverage.** A control with no evidence backing is shown as an
  explicit task to _establish_ that evidence, flagged **no evidence yet** — never as
  something already done.
- **Every task is cited.** Each task references a real control (and, where relevant,
  SCF anchor / policy) that exists in _your_ tenant. If a generated task cites
  anything that cannot be verified against your records, that whole team section is
  withheld rather than shown — you will see a short note explaining why.
- **Nothing is authoritative until you approve it.** The checklist is a draft.
  Approval is **one click per team section**, and the markdown export is disabled
  until at least one section is approved. The tool never approves its own output.
- **Your data stays local.** Generation runs on the local Ollama model by default —
  no control text leaves your deployment. (A cloud-model opt-in is a future option;
  when it exists, a visible banner will tell you a draft was routed to a cloud model.)

## Using it

1. Open **Controls → Implementation checklist** and click **Generate checklist**.
2. Review each team's section. Tasks marked **no evidence yet** are gaps to close.
3. Edit nothing you disagree with — instead, **approve** the sections you are happy
   with. Each approval records _you_ as the approver.
4. Click **Export approved (markdown)** to download the approved sections as a
   checklist you can paste into your tracker.

## Quality caveat (local model)

The default local model is **Llama 3.1 8B Instruct**, chosen so the generator runs
on commodity hardware (an 8–12 GB GPU) with no data leaving your deployment. It is a
small model: the task wording is a _starting point_, not finished copy. Always read
each task before approving it — that review step is the point of the approval gate.
The deterministic team assignment and the citation guarantees above hold regardless
of model quality; only the task _wording_ depends on the model.

The default-model recommendation is reviewed periodically as local models improve.
