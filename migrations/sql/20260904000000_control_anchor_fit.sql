-- security-atlas — control anchor fit (issue #1736).
--
-- Some controls an auditor tests have no real SCF equivalent (SOC 2 CC1
-- governance and HR: audit committee independence, recruitment and
-- retention, performance reviews). Adopters anchor them to the nearest
-- catch-all SCF control, or leave them unanchored, and until now nothing
-- recorded that the anchor is approximate.
--
-- anchor_fit states how well scf_anchor_id fits the control's intent:
--
--   exact    the anchor covers the control (default; every existing row)
--   partial  the anchor covers the control's intent only in part
--   none     no SCF control fits; the anchor is the nearest catch-all,
--            or it is absent
--
-- anchor_fit_note explains a non-exact fit and is required for one. The
-- bundle validator (internal/control) enforces the same rules with a
-- field-pointing error; the CHECKs are the backstop.
--
-- Coverage computation is unchanged: framework credit still flows only
-- through the anchor.

ALTER TABLE controls
    ADD COLUMN anchor_fit      TEXT NOT NULL DEFAULT 'exact',
    ADD COLUMN anchor_fit_note TEXT NULL;

ALTER TABLE controls
    ADD CONSTRAINT controls_anchor_fit_valid
        CHECK (anchor_fit IN ('exact', 'partial', 'none')),
    ADD CONSTRAINT controls_anchor_fit_note_required
        CHECK (anchor_fit = 'exact' OR btrim(coalesce(anchor_fit_note, '')) <> '');
