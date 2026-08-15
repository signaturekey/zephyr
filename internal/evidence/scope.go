package evidence

import (
	"fmt"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/protocol"
)

type Category string

const (
	CategoryCorrectness                   Category = "correctness"
	CategoryFunctionalCorrectness         Category = "functional-correctness"
	CategoryControlFlow                   Category = "control-flow"
	CategoryDataFlow                      Category = "data-flow"
	CategoryErrorHandling                 Category = "error-handling"
	CategoryEdgeCase                      Category = "edge-case"
	CategoryBusinessInvariant             Category = "business-invariant"
	CategoryContextPropagation            Category = "context-propagation"
	CategoryErrorSemantics                Category = "error-semantics"
	CategoryConcurrency                   Category = "concurrency"
	CategoryResourceLifetime              Category = "resource-lifetime"
	CategoryGoAPIDesign                   Category = "go-api-design"
	CategoryNilZeroSemantics              Category = "nil-zero-semantics"
	CategoryRuntimeSafety                 Category = "runtime-safety"
	CategoryAsyncConcurrency              Category = "async-concurrency"
	CategoryTypingRuntimeSemantics        Category = "typing-runtime-semantics"
	CategoryMutableStateSemantics         Category = "mutable-state-semantics"
	CategoryImportRuntimeSemantics        Category = "import-runtime-semantics"
	CategoryFrameworkRuntimeSafety        Category = "framework-runtime-safety"
	CategoryTypeSafety                    Category = "type-safety"
	CategoryTypeNarrowing                 Category = "type-narrowing"
	CategoryUnsafeTypeAssertion           Category = "unsafe-type-assertion"
	CategoryAsyncSemantics                Category = "async-semantics"
	CategoryModuleContract                Category = "module-contract"
	CategoryNullabilitySemantics          Category = "nullability-semantics"
	CategoryRuntimeSchemaMismatch         Category = "runtime-schema-mismatch"
	CategoryRenderingCorrectness          Category = "rendering-correctness"
	CategoryAccessibility                 Category = "accessibility"
	CategoryFrontendPerformance           Category = "frontend-performance"
	CategoryBrowserSecurity               Category = "browser-security"
	CategoryUIResilience                  Category = "ui-resilience"
	CategoryFrontendRouting               Category = "frontend-routing"
	CategoryReactHooks                    Category = "react-hooks"
	CategoryReactLifecycle                Category = "react-lifecycle"
	CategoryReactRendering                Category = "react-rendering"
	CategoryReactStateManagement          Category = "react-state-management"
	CategoryReactServerState              Category = "react-server-state"
	CategoryReactConcurrency              Category = "react-concurrency"
	CategoryReactLibraryIntegration       Category = "react-library-integration"
	CategorySkillFrontmatter              Category = "skill-frontmatter"
	CategorySkillTriggering               Category = "skill-triggering"
	CategoryInstructionCorrectness        Category = "instruction-correctness"
	CategoryProgressiveDisclosure         Category = "progressive-disclosure"
	CategoryToolContract                  Category = "tool-contract"
	CategoryReferenceIntegrity            Category = "reference-integrity"
	CategoryEvaluationCoverage            Category = "evaluation-coverage"
	CategorySkillStructure                Category = "skill-structure"
	CategoryWorkflowSafety                Category = "workflow-safety"
	CategoryContextEfficiency             Category = "context-efficiency"
	CategoryTimeoutPolicy                 Category = "timeout-policy"
	CategoryRetryPolicy                   Category = "retry-policy"
	CategoryIdempotency                   Category = "idempotency"
	CategoryBackpressure                  Category = "backpressure"
	CategoryGracefulDegradation           Category = "graceful-degradation"
	CategoryAvailabilityRisk              Category = "availability-risk"
	CategoryShutdownSafety                Category = "shutdown-safety"
	CategoryOperationalObservability      Category = "operational-observability"
	CategoryDeliverySemantics             Category = "delivery-semantics"
	CategoryMessageOrdering               Category = "message-ordering"
	CategoryMessageDeduplication          Category = "message-deduplication"
	CategoryConsumerState                 Category = "consumer-state"
	CategoryMessageRetryDLQ               Category = "message-retry-dlq"
	CategoryPoisonMessage                 Category = "poison-message"
	CategoryMessageRollout                Category = "message-rollout"
	CategoryMessageBackpressure           Category = "message-backpressure"
	CategoryTransactionalMessaging        Category = "transactional-messaging"
	CategoryDeploymentConfiguration       Category = "deployment-configuration"
	CategoryHealthProbe                   Category = "health-probe"
	CategoryResourcePolicy                Category = "resource-policy"
	CategoryInfrastructureRollout         Category = "infrastructure-rollout"
	CategoryRuntimeConfiguration          Category = "runtime-configuration"
	CategoryCICDSafety                    Category = "ci-cd-safety"
	CategoryWorkloadIsolation             Category = "workload-isolation"
	CategoryInfrastructureDrift           Category = "infrastructure-drift"
	CategoryCacheConsistency              Category = "cache-consistency"
	CategoryCacheInvalidation             Category = "cache-invalidation"
	CategoryTTLSemantics                  Category = "ttl-semantics"
	CategoryStorageConsistency            Category = "storage-consistency"
	CategorySearchIndexMapping            Category = "search-index-mapping"
	CategoryStorageBackfill               Category = "storage-backfill"
	CategoryDataLifecycle                 Category = "data-lifecycle"
	CategoryStorageCapacity               Category = "storage-capacity"
	CategoryStorageFallback               Category = "storage-fallback"
	CategoryAuthentication                Category = "authentication"
	CategoryAuthorization                 Category = "authorization"
	CategoryIDOR                          Category = "idor"
	CategoryInputValidation               Category = "input-validation"
	CategoryInjection                     Category = "injection"
	CategorySecretExposure                Category = "secret-exposure"
	CategorySensitiveDataExposure         Category = "sensitive-data-exposure"
	CategoryUnsafeLogging                 Category = "unsafe-logging"
	CategoryPathFileSecurity              Category = "path-file-security"
	CategoryNetworkSecurity               Category = "network-security"
	CategoryPrivilegeBoundary             Category = "privilege-boundary"
	CategorySQLCorrectness                Category = "sql-correctness"
	CategoryTransactionSafety             Category = "transaction-safety"
	CategoryIsolation                     Category = "isolation"
	CategoryLocking                       Category = "locking"
	CategoryIndexing                      Category = "indexing"
	CategoryMigrationSafety               Category = "migration-safety"
	CategoryDataIntegrity                 Category = "data-integrity"
	CategoryQueryAmplification            Category = "query-amplification"
	CategoryRollbackSafety                Category = "rollback-safety"
	CategoryContractSchema                Category = "contract-schema"
	CategoryAPICompatibility              Category = "api-compatibility"
	CategoryOptionalitySemantics          Category = "optionality-semantics"
	CategoryEnumEvolution                 Category = "enum-evolution"
	CategoryEventCompatibility            Category = "event-compatibility"
	CategoryProducerConsumerCompatibility Category = "producer-consumer-compatibility"
	CategoryGeneratedBoundary             Category = "generated-boundary"
	CategoryTestCoverage                  Category = "test-coverage"
	CategoryNegativeTesting               Category = "negative-testing"
	CategoryBoundaryTesting               Category = "boundary-testing"
	CategoryFailureTesting                Category = "failure-testing"
	CategoryAcceptanceCriteria            Category = "acceptance-criteria"
	CategoryAssertionQuality              Category = "assertion-quality"
	CategoryWrongTestPath                 Category = "wrong-test-path"
	CategoryPlanCompleteness              Category = "plan-completeness"
	CategoryRequirementsAlignment         Category = "requirements-alignment"
	CategoryBoundaryDesign                Category = "boundary-design"
	CategoryDependencyDirection           Category = "dependency-direction"
	CategoryCrossServiceImpact            Category = "cross-service-impact"
	CategoryArchitectureAlignment         Category = "architecture-alignment"
	CategoryRolloutSafety                 Category = "rollout-safety"
	CategorySystemFailureMode             Category = "system-failure-mode"
	CategoryUnnecessaryAbstraction        Category = "unnecessary-abstraction"
	CategoryDuplication                   Category = "duplication"
	CategoryAvoidableComplexity           Category = "avoidable-complexity"
	CategoryLifecycleComplexity           Category = "lifecycle-complexity"
	CategoryMaintainabilityRisk           Category = "maintainability-risk"
)

const (
	ReasonRoleCategoryOutOfScope = "role-category-out-of-scope"
	ReasonRoleLocationOutOfScope = "role-location-out-of-scope"
)

type LocationKind uint8

const (
	LocationCode LocationKind = 1 << iota
	LocationArtifact
)

type reviewerScope struct {
	categories []Category
	locations  LocationKind
}

func reviewerScopeForRole(role string) (reviewerScope, bool) {
	switch role {
	case config.RoleCodeReviewer:
		return reviewerScope{
			categories: []Category{
				CategoryCorrectness,
				CategoryFunctionalCorrectness,
				CategoryControlFlow,
				CategoryDataFlow,
				CategoryErrorHandling,
				CategoryEdgeCase,
				CategoryBusinessInvariant,
			},
			locations: LocationCode,
		}, true
	case config.RoleArchitectReviewer:
		return reviewerScope{
			categories: []Category{
				CategoryPlanCompleteness,
				CategoryRequirementsAlignment,
				CategoryBoundaryDesign,
				CategoryDependencyDirection,
				CategoryCrossServiceImpact,
				CategoryArchitectureAlignment,
				CategoryRolloutSafety,
				CategoryRollbackSafety,
				CategorySystemFailureMode,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleGolangExpert:
		return reviewerScope{
			categories: []Category{
				CategoryCorrectness,
				CategoryContextPropagation,
				CategoryErrorSemantics,
				CategoryConcurrency,
				CategoryResourceLifetime,
				CategoryGoAPIDesign,
				CategoryNilZeroSemantics,
				CategoryRuntimeSafety,
			},
			locations: LocationCode,
		}, true
	case config.RolePythonExpert:
		return reviewerScope{
			categories: []Category{
				CategoryCorrectness,
				CategoryAsyncConcurrency,
				CategoryErrorSemantics,
				CategoryTypingRuntimeSemantics,
				CategoryMutableStateSemantics,
				CategoryResourceLifetime,
				CategoryImportRuntimeSemantics,
				CategoryFrameworkRuntimeSafety,
			},
			locations: LocationCode,
		}, true
	case config.RoleTypeScriptExpert:
		return reviewerScope{
			categories: []Category{
				CategoryCorrectness,
				CategoryTypeSafety,
				CategoryTypeNarrowing,
				CategoryUnsafeTypeAssertion,
				CategoryAsyncSemantics,
				CategoryModuleContract,
				CategoryNullabilitySemantics,
				CategoryRuntimeSchemaMismatch,
			},
			locations: LocationCode,
		}, true
	case config.RoleFrontendExpert:
		return reviewerScope{
			categories: []Category{
				CategoryCorrectness,
				CategoryRenderingCorrectness,
				CategoryAccessibility,
				CategoryFrontendPerformance,
				CategoryBrowserSecurity,
				CategoryUIResilience,
				CategoryFrontendRouting,
			},
			locations: LocationCode,
		}, true
	case config.RoleReactExpert:
		return reviewerScope{
			categories: []Category{
				CategoryCorrectness,
				CategoryReactHooks,
				CategoryReactLifecycle,
				CategoryReactRendering,
				CategoryReactStateManagement,
				CategoryReactServerState,
				CategoryReactConcurrency,
				CategoryReactLibraryIntegration,
			},
			locations: LocationCode,
		}, true
	case config.RoleSkillAuthoringExpert:
		return reviewerScope{
			categories: []Category{
				CategorySkillFrontmatter,
				CategorySkillTriggering,
				CategoryInstructionCorrectness,
				CategoryProgressiveDisclosure,
				CategoryToolContract,
				CategoryReferenceIntegrity,
				CategoryEvaluationCoverage,
				CategorySkillStructure,
				CategoryWorkflowSafety,
				CategoryContextEfficiency,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleReliabilityExpert:
		return reviewerScope{
			categories: []Category{
				CategoryTimeoutPolicy,
				CategoryRetryPolicy,
				CategoryIdempotency,
				CategoryBackpressure,
				CategoryGracefulDegradation,
				CategoryAvailabilityRisk,
				CategoryShutdownSafety,
				CategoryOperationalObservability,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleMessagingExpert:
		return reviewerScope{
			categories: []Category{
				CategoryDeliverySemantics,
				CategoryMessageOrdering,
				CategoryMessageDeduplication,
				CategoryConsumerState,
				CategoryMessageRetryDLQ,
				CategoryPoisonMessage,
				CategoryMessageRollout,
				CategoryMessageBackpressure,
				CategoryTransactionalMessaging,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleInfrastructureExpert:
		return reviewerScope{
			categories: []Category{
				CategoryDeploymentConfiguration,
				CategoryHealthProbe,
				CategoryResourcePolicy,
				CategoryInfrastructureRollout,
				CategoryRuntimeConfiguration,
				CategoryCICDSafety,
				CategoryWorkloadIsolation,
				CategoryInfrastructureDrift,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleStorageExpert:
		return reviewerScope{
			categories: []Category{
				CategoryCacheConsistency,
				CategoryCacheInvalidation,
				CategoryTTLSemantics,
				CategoryStorageConsistency,
				CategorySearchIndexMapping,
				CategoryStorageBackfill,
				CategoryDataLifecycle,
				CategoryStorageCapacity,
				CategoryStorageFallback,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleSecurityAuditor:
		return reviewerScope{
			categories: []Category{
				CategoryAuthentication,
				CategoryAuthorization,
				CategoryIDOR,
				CategoryInputValidation,
				CategoryInjection,
				CategorySecretExposure,
				CategorySensitiveDataExposure,
				CategoryUnsafeLogging,
				CategoryPathFileSecurity,
				CategoryNetworkSecurity,
				CategoryPrivilegeBoundary,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleSQLExpert:
		return reviewerScope{
			categories: []Category{
				CategorySQLCorrectness,
				CategoryTransactionSafety,
				CategoryIsolation,
				CategoryLocking,
				CategoryIndexing,
				CategoryMigrationSafety,
				CategoryDataIntegrity,
				CategoryQueryAmplification,
				CategoryRollbackSafety,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleContractReviewer:
		return reviewerScope{
			categories: []Category{
				CategoryContractSchema,
				CategoryAPICompatibility,
				CategoryOptionalitySemantics,
				CategoryEnumEvolution,
				CategoryEventCompatibility,
				CategoryProducerConsumerCompatibility,
				CategoryGeneratedBoundary,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleQAExpert:
		return reviewerScope{
			categories: []Category{
				CategoryTestCoverage,
				CategoryNegativeTesting,
				CategoryBoundaryTesting,
				CategoryFailureTesting,
				CategoryAcceptanceCriteria,
				CategoryAssertionQuality,
				CategoryWrongTestPath,
			},
			locations: LocationCode | LocationArtifact,
		}, true
	case config.RoleCodeSimplifier:
		return reviewerScope{
			categories: []Category{
				CategoryUnnecessaryAbstraction,
				CategoryDuplication,
				CategoryAvoidableComplexity,
				CategoryLifecycleComplexity,
				CategoryMaintainabilityRisk,
			},
			locations: LocationCode,
		}, true
	default:
		return reviewerScope{}, false
	}
}

func validateReviewerScope(finding protocol.CandidateFinding) (string, string) {
	scope, known := reviewerScopeForRole(finding.Role)
	if !known {
		return "", ""
	}

	category := Category(finding.Category)
	if !containsCategory(scope.categories, category) {
		return ReasonRoleCategoryOutOfScope, fmt.Sprintf(
			"category %q is outside reviewer role %q; allowed categories: %s",
			finding.Category,
			finding.Role,
			joinCategories(scope.categories),
		)
	}

	var location LocationKind
	switch {
	case finding.Location.IsCode() && !finding.Location.IsArtifact():
		location = LocationCode
	case finding.Location.IsArtifact() && !finding.Location.IsCode():
		location = LocationArtifact
	default:
		return "", ""
	}
	if scope.locations&location == 0 {
		return ReasonRoleLocationOutOfScope, fmt.Sprintf(
			"reviewer role %q does not allow %s locations",
			finding.Role,
			locationName(location),
		)
	}
	return "", ""
}

func containsCategory(categories []Category, target Category) bool {
	for _, category := range categories {
		if category == target {
			return true
		}
	}
	return false
}

func joinCategories(categories []Category) string {
	values := make([]string, 0, len(categories))
	for _, category := range categories {
		values = append(values, string(category))
	}
	return strings.Join(values, ", ")
}

func locationName(location LocationKind) string {
	if location == LocationCode {
		return "code"
	}
	return "artifact"
}
