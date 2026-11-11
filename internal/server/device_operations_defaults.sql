-- Apply only at device creation; preserve all existing and customized profiles.
CREATE TRIGGER IF NOT EXISTS device_operations_new_device_defaults
AFTER INSERT ON devices
BEGIN
    INSERT INTO device_operations(device, username, root)
    VALUES (NEW.id, 'nvidia', '/');
END;
