package etcdoperatortask

// Test cases:
// 1) Update is not allowed
// 2) Delete is allowed for exempt service accounts
// 3) Create is allowed only if the config field has only one field set
// 4) Create is not allowed if the appropriate handler is not implemented