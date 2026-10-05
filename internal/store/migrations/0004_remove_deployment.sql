-- A down issued to remove a deployment points at it; once the down is
-- applied, the removed deployment's row is deleted.
ALTER TABLE deployments
    ADD COLUMN removes_deployment_id BIGINT REFERENCES deployments (id) ON DELETE SET NULL;
