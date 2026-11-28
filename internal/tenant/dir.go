package tenant

// Dir returns the data folder this install was opened at. The admin
// accounts keep their own database under it, in admin/admin.db (P15-01,
// decision 0650), apart from every tenant's folder.
func (in *Install) Dir() string { return in.dir }
