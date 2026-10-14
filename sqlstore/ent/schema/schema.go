// Package schema is the Ent schema of connectforge's SQL store. Every table
// is prefixed connectforge_ and carries owner_id, except the lease table,
// whose keys are not owned.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	cf "github.com/plexusone/connectforge"
)

func table(name string) []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "connectforge_" + name}}
}

func ownerField() ent.Field {
	return field.String("owner_id").NotEmpty().Immutable()
}

// Connection is an owner's relationship with an account at a provider.
type Connection struct{ ent.Schema }

// Annotations names the table.
func (Connection) Annotations() []schema.Annotation { return table("connections") }

// Fields of Connection.
func (Connection) Fields() []ent.Field {
	return []ent.Field{
		ownerField(),
		field.String("connection_id").NotEmpty().Immutable(),
		field.String("provider").NotEmpty().Immutable(),
		field.String("auth_method"),
		field.String("display_name"),
		field.String("external_account_id").Default(""),
		field.String("external_label").Default(""),
		field.JSON("scopes", []string{}).Optional(),
		field.String("state"),
		field.JSON("health", cf.Health{}),
		field.JSON("metadata", map[string]string{}).Optional(),
		field.String("created_by_kind"),
		field.String("created_by_id"),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
		field.Uint64("revision"),
	}
}

// Indexes of Connection.
func (Connection) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_id", "connection_id").Unique(),
		index.Fields("owner_id", "provider"),
	}
}

// Credential is one sealed version of a connection's credential. The active
// version is the highest; versions are only ever added one above it.
type Credential struct{ ent.Schema }

// Annotations names the table.
func (Credential) Annotations() []schema.Annotation { return table("credentials") }

// Fields of Credential.
func (Credential) Fields() []ent.Field {
	return []ent.Field{
		ownerField(),
		field.String("connection_id").NotEmpty().Immutable(),
		field.Int("version").Positive().Immutable(),
		field.String("kind").Immutable(),
		field.Bytes("ciphertext").Immutable(),
		field.Time("expires_at").Optional().Nillable().Immutable(),
		field.Time("created_at").Immutable(),
	}
}

// Indexes of Credential.
func (Credential) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_id", "connection_id", "version").Unique()}
}

// Grant is what a principal may do with a connection.
type Grant struct{ ent.Schema }

// Annotations names the table.
func (Grant) Annotations() []schema.Annotation { return table("grants") }

// Fields of Grant.
func (Grant) Fields() []ent.Field {
	return []ent.Field{
		ownerField(),
		field.String("connection_id").NotEmpty().Immutable(),
		field.String("principal_kind").Immutable(),
		field.String("principal_id").Immutable(),
		field.JSON("actions", []string{}),
	}
}

// Indexes of Grant.
func (Grant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_id", "connection_id", "principal_kind", "principal_id").Unique()}
}

// Binding is a consumer that uses a connection for a purpose.
type Binding struct{ ent.Schema }

// Annotations names the table.
func (Binding) Annotations() []schema.Annotation { return table("bindings") }

// Fields of Binding.
func (Binding) Fields() []ent.Field {
	return []ent.Field{
		ownerField(),
		field.String("connection_id").NotEmpty().Immutable(),
		field.String("consumer_kind").Immutable(),
		field.String("consumer_id").Immutable(),
		field.String("purpose").Immutable(),
		field.Time("created_at").Immutable(),
	}
}

// Indexes of Binding.
func (Binding) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_id", "connection_id", "consumer_kind", "consumer_id", "purpose").Unique()}
}

// Lease is a short exclusive lease, such as the one that makes a refresh
// single-flight. Its key is not owned, so it carries no owner_id.
type Lease struct{ ent.Schema }

// Annotations names the table.
func (Lease) Annotations() []schema.Annotation { return table("leases") }

// Fields of Lease.
func (Lease) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().Unique().Immutable(),
		field.String("token"),
		field.Time("expires_at"),
	}
}
