package main

import "time"

// Workspace ...
type Workspace struct {
	ID                   string    `db:"id" json:"id"`
	Name                 string    `db:"name" json:"name"`
	CreatedAt            time.Time `db:"created_at" json:"createdAt"`
	AllowExternalSharing bool      `db:"allow_external_sharing" json:"allowExternalSharing"`
	ExternalCustomerID   string    `db:"external_customer_id" json:"-"`
	EUVAT                string    `db:"eu_vat" json:"euVat"`
	ExternalBillingEmail string    `db:"external_billing_email" json:"externalBillingEmail"`
}

// Account ...
type Account struct {
	ID                       string    `db:"id" json:"id"`
	Name                     string    `db:"name" json:"name"`
	Email                    string    `db:"email" json:"email"`
	Password                 string    `db:"password" json:"-"`
	CreatedAt                time.Time `db:"created_at" json:"createdAt"`
	EmailConfirmed           bool      `db:"email_confirmed" json:"emailConfirmed"`
	EmailConfirmationSentTo  string    `db:"email_confirmation_sent_to" json:"emailConfirmationSentTo"`
	EmailConfirmationKey     string    `db:"email_confirmation_key" json:"-"`
	EmailConfirmationPending bool      `db:"email_confirmation_pending" json:"emailConfirmationPending"`
	PasswordResetKey         string    `db:"password_reset_key" json:"-"`
	LatestActivity           time.Time `db:"latest_activity" json:"-"`
}

// Subscription ...
type Subscription struct {
	WorkspaceID                string    `db:"workspace_id" json:"workspaceId"`
	ID                         string    `db:"id" json:"id"`
	Level                      string    `db:"level" json:"level"`
	NumberOfEditors            int       `db:"number_of_editors" json:"numberOfEditors"`
	FromDate                   time.Time `db:"from_date" json:"fromDate"`
	ExpirationDate             time.Time `db:"expiration_date" json:"expirationDate"`
	CreatedByName              string    `db:"created_by_name" json:"createdByName"`
	CreatedAt                  time.Time `db:"created_at" json:"createdAt"`
	LastModified               time.Time `db:"last_modified" json:"lastModified"`
	LastModifiedByName         string    `db:"last_modified_by_name" json:"lastModifiedByName"`
	Status                     string    `db:"status" json:"externalStatus"`
	ExternalCustomerID         string    `db:"external_customer_id" json:"-"`
	ExternalPlanID             string    `db:"external_plan_id" json:"-"`
	ExternalSubscriptionID     string    `db:"external_subscription_id" json:"-"`
	ExternalSubscriptionItemID string    `db:"external_subscription_item_id" json:"-"`
}

// Member ...
type Member struct {
	ID          string    `db:"id" json:"id"`
	WorkspaceID string    `db:"workspace_id" json:"workspaceId"`
	AccountID   string    `db:"account_id" json:"accountId"`
	Level       string    `db:"level" json:"level"`
	Name        string    `db:"name" json:"name"`   // Joined in
	Email       string    `db:"email" json:"email"` // Joined in
	CreatedAt   time.Time `db:"created_at" json:"createdAt"`
}

// Invite ...
type Invite struct {
	WorkspaceID    string    `db:"workspace_id" json:"workspaceId"`
	ID             string    `db:"id" json:"id"`
	Email          string    `db:"email" json:"email"`
	Level          string    `db:"level" json:"level"`
	Code           string    `db:"code" json:"code"`
	CreatedBy      string    `db:"created_by" json:"createdBy"`
	CreatedByName  string    `db:"created_by_name" json:"createdByName"`
	CreatedAt      time.Time `db:"created_at" json:"createdAt"`
	CreatedByEmail string    `db:"created_by_email" json:"createdByEmail"`
	WorkspaceName  string    `db:"workspace_name" json:"workspaceName"`
}

// Project ...
type Project struct {
	WorkspaceID        string    `db:"workspace_id" json:"workspaceId"`
	ID                 string    `db:"id" json:"id"`
	Title              string    `db:"title" json:"title"`
	Description        string    `db:"description" json:"description"`
	CreatedByName      string    `db:"created_by_name" json:"createdByName"`
	CreatedAt          time.Time `db:"created_at" json:"createdAt"`
	LastModified       time.Time `db:"last_modified" json:"lastModified"`
	LastModifiedByName string    `db:"last_modified_by_name" json:"lastModifiedByName"`
	ExternalLink       string    `db:"external_link" json:"externalLink"`
	Annotations        string    `db:"annotations" json:"annotations"`
}

// Milestone ...
type Milestone struct {
	WorkspaceID        string    `db:"workspace_id" json:"workspaceId"`
	ProjectID          string    `db:"project_id" json:"projectId"`
	ID                 string    `db:"id" json:"id"`
	Title              string    `db:"title" json:"title"`
	Description        string    `db:"description" json:"description"`
	Status             string    `db:"status" json:"status"`
	Rank               string    `db:"rank" json:"rank"`
	CreatedByName      string    `db:"created_by_name" json:"createdByName"`
	CreatedAt          time.Time `db:"created_at" json:"createdAt"`
	LastModified       time.Time `db:"last_modified" json:"lastModified"`
	LastModifiedByName string    `db:"last_modified_by_name" json:"lastModifiedByName"`
	Color              string    `db:"color" json:"color"`
	Annotations        string    `db:"annotations" json:"annotations"`
}

// Workflow ...
type Workflow struct {
	WorkspaceID        string    `db:"workspace_id" json:"workspaceId"`
	ProjectID          string    `db:"project_id" json:"projectId"`
	ID                 string    `db:"id" json:"id"`
	Title              string    `db:"title" json:"title"`
	Description        string    `db:"description" json:"description"`
	Rank               string    `db:"rank" json:"rank"`
	CreatedByName      string    `db:"created_by_name" json:"createdByName"`
	CreatedAt          time.Time `db:"created_at" json:"createdAt"`
	LastModified       time.Time `db:"last_modified" json:"lastModified"`
	LastModifiedByName string    `db:"last_modified_by_name" json:"lastModifiedByName"`
	Color              string    `db:"color" json:"color"`
	Status             string    `db:"status" json:"status"`
	Annotations        string    `db:"annotations" json:"annotations"`
}

// SubWorkflow ...
type SubWorkflow struct {
	WorkspaceID        string    `db:"workspace_id" json:"workspaceId"`
	WorkflowID         string    `db:"workflow_id" json:"workflowId"`
	ID                 string    `db:"id" json:"id"`
	Title              string    `db:"title" json:"title"`
	Description        string    `db:"description" json:"description"`
	Rank               string    `db:"rank" json:"rank"`
	CreatedByName      string    `db:"created_by_name" json:"createdByName"`
	CreatedAt          time.Time `db:"created_at" json:"createdAt"`
	LastModified       time.Time `db:"last_modified" json:"lastModified"`
	LastModifiedByName string    `db:"last_modified_by_name" json:"lastModifiedByName"`
	Color              string    `db:"color" json:"color"`
	Status             string    `db:"status" json:"status"`
	Annotations        string    `db:"annotations" json:"annotations"`
}

// Feature ...
type Feature struct {
	WorkspaceID        string    `db:"workspace_id" json:"workspaceId"`
	SubWorkflowID      string    `db:"subworkflow_id" json:"subWorkflowId"`
	MilestoneID        string    `db:"milestone_id" json:"milestoneId"`
	ID                 string    `db:"id" json:"id"`
	Title              string    `db:"title" json:"title"`
	Rank               string    `db:"rank" json:"rank"`
	Description        string    `db:"description" json:"description"`
	Status             string    `db:"status" json:"status"`
	CreatedByName      string    `db:"created_by_name" json:"createdByName"`
	CreatedAt          time.Time `db:"created_at" json:"createdAt"`
	LastModified       time.Time `db:"last_modified" json:"lastModified"`
	LastModifiedByName string    `db:"last_modified_by_name" json:"lastModifiedByName"`
	Color              string    `db:"color" json:"color"`
	Annotations        string    `db:"annotations" json:"annotations"`
	Estimate           int       `db:"estimate" json:"estimate"`
}

// FeatureComment ...
type FeatureComment struct {
	WorkspaceID   string    `db:"workspace_id" json:"workspaceId"`
	ID            string    `db:"id" json:"id"`
	FeatureID     string    `db:"feature_id" json:"featureId"`
	ProjectID     string    `db:"project_id" json:"projectId"`
	Post          string    `db:"post" json:"post"`
	CreatedByName string    `db:"created_by_name" json:"createdByName"`
	CreatedAt     time.Time `db:"created_at" json:"createdAt"`
	LastModified  time.Time `db:"last_modified" json:"lastModified"`
	MemberID      string    `db:"-" json:"memberId"`
}

// FeatureCommentOwner ...
type FeatureCommentOwner struct {
	WorkspaceID      string `db:"workspace_id" json:"workspaceId"`
	ID               string `db:"id" json:"id"`
	FeatureCommentID string `db:"feature_comment_id" json:"featureCommentId"`
	MemberID         string `db:"member_id" json:"memberId"`
	ProjectID        string `db:"project_id" json:"projectId"`
}

// PlaneConnection -- per-project link to a Plane instance (SYNC-001/002/003).
type PlaneConnection struct {
	WorkspaceID     string    `db:"workspace_id" json:"workspaceId"`
	ID              string    `db:"id" json:"id"`
	ProjectID       string    `db:"project_id" json:"projectId"`
	BaseURL         string    `db:"base_url" json:"baseUrl"`
	PlaneWorkspace  string    `db:"plane_workspace" json:"planeWorkspace"`
	APIKeyCipher    []byte    `db:"api_key_cipher" json:"-"`
	APIKeyNonce     []byte    `db:"api_key_nonce" json:"-"`
	APIKeyHint      string    `db:"api_key_hint" json:"apiKeyHint"`
	WatchedProjects string    `db:"watched_projects" json:"watchedProjects"`
	CreatedAt       time.Time `db:"created_at" json:"createdAt"`
	LastModified    time.Time `db:"last_modified" json:"lastModified"`
}

// PlaneLink -- a feature card linked to a Plane work item (SYNC-010/023/040).
type PlaneLink struct {
	WorkspaceID      string     `db:"workspace_id" json:"workspaceId"`
	ID               string     `db:"id" json:"id"`
	ProjectID        string     `db:"project_id" json:"projectId"`
	FeatureID        string     `db:"feature_id" json:"featureId"`
	PlaneProjectID   string     `db:"plane_project_id" json:"planeProjectId"`
	PlaneWorkItemID  string     `db:"plane_work_item_id" json:"planeWorkItemId"`
	LastPulledCursor string     `db:"last_pulled_cursor" json:"lastPulledCursor"`
	LastSyncedAt     *time.Time `db:"last_synced_at" json:"lastSyncedAt"`
	LastStatus       string     `db:"last_status" json:"lastStatus"`
	LastError        string     `db:"last_error" json:"lastError"`
}

// PlaneCommentMap -- maps Featmap<->Plane comments + origin (SYNC-020/021/022).
type PlaneCommentMap struct {
	WorkspaceID      string     `db:"workspace_id" json:"workspaceId"`
	ID               string     `db:"id" json:"id"`
	LinkID           string     `db:"link_id" json:"linkId"`
	FeatmapCommentID *string    `db:"featmap_comment_id" json:"featmapCommentId"`
	PlaneCommentID   *string    `db:"plane_comment_id" json:"planeCommentId"`
	Origin           string     `db:"origin" json:"origin"`
	PlaneUpdatedAt   *time.Time `db:"plane_updated_at" json:"planeUpdatedAt"`
	CreatedAt        time.Time  `db:"created_at" json:"createdAt"`
}

// Persona ...
type Persona struct {
	WorkspaceID string    `db:"workspace_id" json:"workspaceId"`
	ProjectID   string    `db:"project_id" json:"projectId"`
	ID          string    `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Role        string    `db:"role" json:"role"`
	Avatar      string    `db:"avatar" json:"avatar"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at" json:"createdAt"`
}

// WorkflowPersona ...
type WorkflowPersona struct {
	WorkspaceID string `db:"workspace_id" json:"workspaceId"`
	ProjectID   string `db:"project_id" json:"projectId"`
	WorkflowID  string `db:"workflow_id" json:"workflowId"`
	ID          string `db:"id" json:"id"`
	PersonaID   string `db:"persona_id" json:"personaId"`
}

// APIKey ...
type APIKey struct {
	ID          string     `db:"id" json:"id"`
	AccountID   string     `db:"account_id" json:"-"`
	Name        string     `db:"name" json:"name"`
	KeyHash     string     `db:"key_hash" json:"-"`
	KeyPrefix   string     `db:"key_prefix" json:"keyPrefix"`
	CreatedAt   time.Time  `db:"created_at" json:"createdAt"`
	LastUsedAt  *time.Time `db:"last_used_at" json:"lastUsedAt,omitempty"`
	ExpiresAt   *time.Time `db:"expires_at" json:"expiresAt,omitempty"`
	RevokedAt   *time.Time `db:"revoked_at" json:"revokedAt,omitempty"`
}
