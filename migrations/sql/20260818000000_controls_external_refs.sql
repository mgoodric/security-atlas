-- security-atlas -- #1737: external references on control versions.
--
-- `external_refs` carries the `external_refs[]` list from control.yaml: the
-- organisation's own control numbers, auditor references, and the framework
-- requirements a control was tested under. Each element is
-- {system, kind, id, note?}; kind is 'reference' or 'tested_under'. The
-- bundle parser (internal/control/manifest.go) validates the element shape;
-- the column only pins that it is an array, so an older or hand-written row
-- can never hold a scalar the readers would have to special-case.
--
-- Stored per control version like evidence_queries: a supersession writes
-- the new list onto the new row and the old row keeps the old one. Existing
-- rows take '[]', which is what a bundle without the field produces.
--
-- RLS: `controls` is already tenant-scoped; a new column inherits it.

ALTER TABLE controls
    ADD COLUMN external_refs JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE controls ADD CONSTRAINT controls_external_refs_array_chk
    CHECK (jsonb_typeof(external_refs) = 'array');

COMMENT ON COLUMN controls.external_refs IS
    'control.yaml external_refs[]: [{system, kind, id, note?}], kind in '
    '(reference, tested_under). Validated by the bundle parser (#1737).';
