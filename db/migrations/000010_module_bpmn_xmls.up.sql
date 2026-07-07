ALTER TABLE workflow_version
    ADD COLUMN module_bpmn_xmls TEXT[] NOT NULL DEFAULT '{}';
