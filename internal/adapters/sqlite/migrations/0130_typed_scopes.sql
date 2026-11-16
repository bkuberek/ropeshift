-- A decision's scope names what it binds in one grammar: plan:<id>, contract:<id>, wo:<id>, or repo:<id>
-- (ADR-0030). A scope stored before the grammar is a bare ID. One that is exactly the ID of one plan,
-- one contract, one work order, or one repository takes that kind as its prefix. One that is the ID of
-- records of two kinds names no one record, and one that names none is free text. Both stay as written,
-- bind no work, and overlap another such scope by the rule they had. The four statements read only
-- what is stored, and a scope they rewrite holds a colon no ID holds, so no statement meets another's work.

UPDATE decisions SET scope = 'plan:' || scope
 WHERE EXISTS (SELECT 1 FROM plans p WHERE p.workspace_id = decisions.workspace_id AND p.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM contracts c WHERE c.workspace_id = decisions.workspace_id AND c.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM work_orders w WHERE w.workspace_id = decisions.workspace_id AND w.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM repositories r WHERE r.workspace_id = decisions.workspace_id AND r.id = decisions.scope);

UPDATE decisions SET scope = 'contract:' || scope
 WHERE EXISTS (SELECT 1 FROM contracts c WHERE c.workspace_id = decisions.workspace_id AND c.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM plans p WHERE p.workspace_id = decisions.workspace_id AND p.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM work_orders w WHERE w.workspace_id = decisions.workspace_id AND w.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM repositories r WHERE r.workspace_id = decisions.workspace_id AND r.id = decisions.scope);

UPDATE decisions SET scope = 'wo:' || scope
 WHERE EXISTS (SELECT 1 FROM work_orders w WHERE w.workspace_id = decisions.workspace_id AND w.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM plans p WHERE p.workspace_id = decisions.workspace_id AND p.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM contracts c WHERE c.workspace_id = decisions.workspace_id AND c.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM repositories r WHERE r.workspace_id = decisions.workspace_id AND r.id = decisions.scope);

UPDATE decisions SET scope = 'repo:' || scope
 WHERE EXISTS (SELECT 1 FROM repositories r WHERE r.workspace_id = decisions.workspace_id AND r.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM plans p WHERE p.workspace_id = decisions.workspace_id AND p.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM contracts c WHERE c.workspace_id = decisions.workspace_id AND c.id = decisions.scope)
   AND NOT EXISTS (SELECT 1 FROM work_orders w WHERE w.workspace_id = decisions.workspace_id AND w.id = decisions.scope);
