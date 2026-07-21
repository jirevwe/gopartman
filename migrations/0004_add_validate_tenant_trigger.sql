CREATE OR REPLACE FUNCTION partman.validate_tenant_id() RETURNS TRIGGER AS
$$
BEGIN
    IF NEW.tenant_id IS NOT NULL THEN
        IF NOT EXISTS (SELECT 1
                       FROM partman.tenants
                       WHERE parent_table_id = NEW.parent_table_id
                         AND id = NEW.tenant_id) THEN
            RAISE EXCEPTION 'Tenant % does not exist for parent table %',
                NEW.tenant_id, NEW.parent_table_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS validate_tenant_id_trigger ON partman.partitions;

CREATE TRIGGER validate_tenant_id_trigger
    BEFORE INSERT
    ON partman.partitions
    FOR EACH ROW
EXECUTE FUNCTION partman.validate_tenant_id();
