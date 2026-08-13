package evidence

import (
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewerScopeDefinitions(t *testing.T) {
	tests := []struct {
		role       string
		categories []Category
		locations  LocationKind
	}{
		{
			role: config.RoleCodeReviewer,
			categories: []Category{
				CategoryCorrectness, CategoryFunctionalCorrectness, CategoryControlFlow,
				CategoryDataFlow, CategoryErrorHandling, CategoryEdgeCase, CategoryBusinessInvariant,
			},
			locations: LocationCode,
		},
		{
			role: config.RoleArchitectReviewer,
			categories: []Category{
				CategoryPlanCompleteness, CategoryRequirementsAlignment, CategoryBoundaryDesign,
				CategoryDependencyDirection, CategoryCrossServiceImpact, CategoryArchitectureAlignment,
				CategoryRolloutSafety, CategoryRollbackSafety, CategorySystemFailureMode,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleGolangExpert,
			categories: []Category{
				CategoryCorrectness, CategoryContextPropagation, CategoryErrorSemantics, CategoryConcurrency,
				CategoryResourceLifetime, CategoryGoAPIDesign, CategoryNilZeroSemantics, CategoryRuntimeSafety,
			},
			locations: LocationCode,
		},
		{
			role: config.RoleTypeScriptExpert,
			categories: []Category{
				CategoryCorrectness, CategoryTypeSafety, CategoryTypeNarrowing, CategoryUnsafeTypeAssertion,
				CategoryAsyncSemantics, CategoryModuleContract, CategoryNullabilitySemantics, CategoryRuntimeSchemaMismatch,
			},
			locations: LocationCode,
		},
		{
			role: config.RoleFrontendExpert,
			categories: []Category{
				CategoryCorrectness, CategoryReactivity, CategoryComponentLifecycle, CategoryStateManagement,
				CategoryServerState, CategoryRenderingCorrectness, CategoryAccessibility,
				CategoryFrontendPerformance, CategoryBrowserSecurity, CategoryUIResilience, CategoryFrontendRouting,
			},
			locations: LocationCode,
		},
		{
			role: config.RoleSkillAuthoringExpert,
			categories: []Category{
				CategorySkillFrontmatter, CategorySkillTriggering, CategoryInstructionCorrectness,
				CategoryProgressiveDisclosure, CategoryToolContract, CategoryReferenceIntegrity,
				CategoryEvaluationCoverage, CategorySkillStructure, CategoryWorkflowSafety, CategoryContextEfficiency,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleReliabilityExpert,
			categories: []Category{
				CategoryTimeoutPolicy, CategoryRetryPolicy, CategoryIdempotency, CategoryBackpressure,
				CategoryGracefulDegradation, CategoryAvailabilityRisk, CategoryShutdownSafety, CategoryOperationalObservability,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleMessagingExpert,
			categories: []Category{
				CategoryDeliverySemantics, CategoryMessageOrdering, CategoryMessageDeduplication,
				CategoryConsumerState, CategoryMessageRetryDLQ, CategoryPoisonMessage,
				CategoryMessageRollout, CategoryMessageBackpressure, CategoryTransactionalMessaging,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleInfrastructureExpert,
			categories: []Category{
				CategoryDeploymentConfiguration, CategoryHealthProbe, CategoryResourcePolicy,
				CategoryInfrastructureRollout, CategoryRuntimeConfiguration, CategoryCICDSafety,
				CategoryWorkloadIsolation, CategoryInfrastructureDrift,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleStorageExpert,
			categories: []Category{
				CategoryCacheConsistency, CategoryCacheInvalidation, CategoryTTLSemantics,
				CategoryStorageConsistency, CategorySearchIndexMapping, CategoryStorageBackfill,
				CategoryDataLifecycle, CategoryStorageCapacity, CategoryStorageFallback,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleSecurityAuditor,
			categories: []Category{
				CategoryAuthentication, CategoryAuthorization, CategoryIDOR, CategoryInputValidation,
				CategoryInjection, CategorySecretExposure, CategorySensitiveDataExposure, CategoryUnsafeLogging,
				CategoryPathFileSecurity, CategoryNetworkSecurity, CategoryPrivilegeBoundary,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleSQLExpert,
			categories: []Category{
				CategorySQLCorrectness, CategoryTransactionSafety, CategoryIsolation, CategoryLocking,
				CategoryIndexing, CategoryMigrationSafety, CategoryDataIntegrity,
				CategoryQueryAmplification, CategoryRollbackSafety,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleContractReviewer,
			categories: []Category{
				CategoryContractSchema, CategoryAPICompatibility, CategoryOptionalitySemantics,
				CategoryEnumEvolution, CategoryEventCompatibility,
				CategoryProducerConsumerCompatibility, CategoryGeneratedBoundary,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleQAExpert,
			categories: []Category{
				CategoryTestCoverage, CategoryNegativeTesting, CategoryBoundaryTesting,
				CategoryFailureTesting, CategoryAcceptanceCriteria, CategoryAssertionQuality,
				CategoryWrongTestPath,
			},
			locations: LocationCode | LocationArtifact,
		},
		{
			role: config.RoleCodeSimplifier,
			categories: []Category{
				CategoryUnnecessaryAbstraction, CategoryDuplication, CategoryAvoidableComplexity,
				CategoryLifecycleComplexity, CategoryMaintainabilityRisk,
			},
			locations: LocationCode,
		},
	}

	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			scope, ok := reviewerScopeForRole(test.role)
			require.True(t, ok, "role scope is missing")
			if got, want := joinCategories(scope.categories), joinCategories(test.categories); got != want {
				t.Fatalf("categories = %q, want %q", got, want)
			}
			assert.Equal(t, test.locations, scope.locations)
		})
	}

	if _, ok := reviewerScopeForRole("evidence-gate"); ok {
		t.Fatal("evidence-gate must not have a reviewer scope")
	}
}

func TestPrecheckRejectsCategoryOutsideReviewerScope(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	finding := candidate("code-reviewer-001", config.RoleCodeReviewer, schema.SeverityP2)
	finding.Category = string(CategoryMigrationSafety)
	finding.Location = schema.FindingLocation{File: "handler.go", LineStart: 1}

	report := Precheck(
		schema.CandidateEnvelope{
			Version: schema.ProtocolVersion,
			RunID:   "run",
			Role:    config.RoleCodeReviewer,
			Findings: []schema.CandidateFinding{
				finding,
			},
		},
		contextpack.Packet{
			Version:      contextpack.Version,
			RunID:        "run",
			Mode:         "implementation",
			ChangedFiles: []string{"handler.go"},
			Diff:         contextpack.Diff{Full: "+++ b/handler.go\n@@ -0,0 +1 @@\n+package demo\n"},
		},
		cfg,
	)

	if len(report.Accepted) != 0 || len(report.Rejected) != 1 {
		t.Fatalf("unexpected precheck report: %#v", report)
	}
	rejection := report.Rejected[0]
	if rejection.ReasonCode != ReasonRoleCategoryOutOfScope {
		t.Fatalf("reason code = %q, want %q", rejection.ReasonCode, ReasonRoleCategoryOutOfScope)
	}
	if !strings.Contains(rejection.Reason, "allowed categories: correctness, functional-correctness") {
		t.Fatalf("rejection does not expose the stable allowed set: %q", rejection.Reason)
	}
}

func TestPrecheckRejectsLocationOutsideReviewerScope(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	finding := candidate("golang-expert-001", config.RoleGolangExpert, schema.SeverityP2)
	finding.Category = string(CategoryConcurrency)
	finding.Location = schema.FindingLocation{Artifact: "REVIEW_SPEC.md", Section: "Concurrency"}

	report := Precheck(
		schema.CandidateEnvelope{
			Version: schema.ProtocolVersion,
			RunID:   "run",
			Role:    config.RoleGolangExpert,
			Findings: []schema.CandidateFinding{
				finding,
			},
		},
		contextpack.Packet{
			Version: contextpack.Version,
			RunID:   "run",
			Mode:    "alignment",
			Plan:    &contextpack.Document{Path: "REVIEW_SPEC.md", Content: "# Concurrency\n"},
		},
		cfg,
	)

	if len(report.Accepted) != 0 || len(report.Rejected) != 1 {
		t.Fatalf("unexpected precheck report: %#v", report)
	}
	if got := report.Rejected[0].ReasonCode; got != ReasonRoleLocationOutOfScope {
		t.Fatalf("reason code = %q, want %q", got, ReasonRoleLocationOutOfScope)
	}
	if got, want := report.Rejected[0].Reason, `reviewer role "golang-expert" does not allow artifact locations`; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

func TestReviewerScopesAllowDomainRolesToCitePlanArtifacts(t *testing.T) {
	roles := []struct {
		role     string
		category Category
	}{
		{config.RoleArchitectReviewer, CategoryPlanCompleteness},
		{config.RoleSecurityAuditor, CategoryAuthorization},
		{config.RoleSQLExpert, CategoryMigrationSafety},
		{config.RoleContractReviewer, CategoryAPICompatibility},
		{config.RoleQAExpert, CategoryAcceptanceCriteria},
		{config.RoleSkillAuthoringExpert, CategoryInstructionCorrectness},
		{config.RoleReliabilityExpert, CategoryRetryPolicy},
		{config.RoleMessagingExpert, CategoryDeliverySemantics},
		{config.RoleInfrastructureExpert, CategoryHealthProbe},
		{config.RoleStorageExpert, CategoryCacheConsistency},
	}

	for _, role := range roles {
		t.Run(role.role, func(t *testing.T) {
			finding := candidate(role.role+"-001", role.role, schema.SeverityP2)
			finding.Category = string(role.category)
			finding.Location = schema.FindingLocation{Artifact: "REVIEW_SPEC.md", Section: "Scope"}
			if code, reason := validateReviewerScope(finding); code != "" {
				t.Fatalf("unexpected scope rejection %q: %s", code, reason)
			}
		})
	}
}

func TestFrontendAndTypeScriptReviewerScopes(t *testing.T) {
	tests := []struct {
		role     string
		category Category
	}{
		{config.RoleTypeScriptExpert, CategoryUnsafeTypeAssertion},
		{config.RoleFrontendExpert, CategoryComponentLifecycle},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			finding := candidate(test.role+"-001", test.role, schema.SeverityP1)
			finding.Category = string(test.category)
			finding.Location = schema.FindingLocation{File: "src/component.tsx", LineStart: 1}
			if code, reason := validateReviewerScope(finding); code != "" {
				t.Fatalf("unexpected scope rejection %q: %s", code, reason)
			}
		})
	}
}

func TestPythonReviewerScopeAcceptsPythonCodeCategoriesOnly(t *testing.T) {
	scope, known := reviewerScopeForRole("python-expert")
	if !known {
		t.Fatal("python-expert scope is not registered")
	}
	if !containsCategory(scope.categories, Category("async-concurrency")) || scope.locations != LocationCode {
		t.Fatalf("unexpected Python reviewer scope: %#v", scope)
	}
	finding := candidate("python-expert-001", "python-expert", schema.SeverityP2)
	finding.Category = "async-concurrency"
	finding.Location = schema.FindingLocation{File: "service/worker.py", LineStart: 10}
	if code, reason := validateReviewerScope(finding); code != "" {
		t.Fatalf("unexpected scope rejection %q: %s", code, reason)
	}
}
