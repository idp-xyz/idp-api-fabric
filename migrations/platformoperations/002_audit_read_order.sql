-- The audit records of a product are read back, newest first, a page at a time
-- (F06 IN-08). From here the application role also reads the table, which it
-- could only append to; it still changes and removes nothing of it. The
-- privilege is not in this file: what a process may do on a table is declared
-- in ApplicationPrivileges and granted by fabric-migrate -apply -grant-to.
--
-- A list is ordered by the instant a record was recorded and then by its
-- identifier, which tells apart the records of one instant. Neither column
-- ever changes, so a record keeps its place for as long as it is listed, and
-- a page starts behind the last record of the page before. The index is in
-- that order and is read backward, so a page costs what it returns, however
-- far into the trail it is. The records that belong to no product are in no
-- list, and have no place in it.
CREATE INDEX audit_record_list_order
    ON platformoperations.audit_record (product_id, recorded_at, audit_id);

-- What happened in one request is found by the identifier that ties its
-- records, its log lines and its events together. It names a handful of
-- records among all of a product, so the list in the order above would read
-- the whole of the trail to find them.
CREATE INDEX audit_record_by_correlation
    ON platformoperations.audit_record (product_id, correlation_id);
