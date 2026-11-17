---
id: questionnaires
title: Questionnaires
routes: [/questionnaires]
terms: [questionnaire, anchor, prior-answer]
diagrams: []
---

Customers often send a security questionnaire before they buy: a
spreadsheet of questions about how you protect their data. Answering
them from scratch every time is slow and the answers drift apart. Here
you answer each one from your real controls, policies and evidence, and
reuse earlier answers.

## How it works

Upload the questionnaire as an Excel workbook (.xlsx, up to 5 MB). Its
questions are read from the sheet and grouped by domain. Open one to
answer it: a quick yes, no or not applicable, a written answer, and
**+ Cite** to point at the controls or evidence that back it up.

Questions linked to an SCF anchor show up to three earlier answers to
the same anchor; **Use this answer** copies one in, and you can save a
good answer to the library for next time.

## Suggested answers

**Suggest answer (AI)** drafts an answer using the AI model your
deployment runs, by default one on your own servers. Every draft cites
specific evidence or policies, and a draft whose citations don't match
your real records is thrown away. With nothing to cite, it tells you to
answer by hand. A suggested answer is never used until a person
approves it, one answer at a time.

When finished, **Export PDF** produces the answered questionnaire.

## Who can do this

Admins upload questionnaires, write answers and run and approve
suggestions. GRC engineers can read them. Other roles don't see this
page.
