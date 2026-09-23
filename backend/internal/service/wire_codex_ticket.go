package service

import "github.com/Wei-Shaw/sub2api/internal/config"

// Construct the harvester only after history and proxy settings are available.
func ProvideOpenAIGatewayService(
	accountRepo AccountRepository,
	usageLogRepo UsageLogRepository,
	usageBillingRepo UsageBillingRepository,
	userRepo UserRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *SchedulerSnapshotService,
	concurrencyService *ConcurrencyService,
	billingService *BillingService,
	rateLimitService *RateLimitService,
	billingCacheService *BillingCacheService,
	httpUpstream HTTPUpstream,
	deferredService *DeferredService,
	openAITokenProvider *OpenAITokenProvider,
	grokTokenProvider *GrokTokenProvider,
	resolver *ModelPricingResolver,
	channelService *ChannelService,
	balanceNotifyService *BalanceNotifyService,
	settingService *SettingService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
	history CodexTicketAttemptRepository,
) *OpenAIGatewayService {
	s := NewOpenAIGatewayService(accountRepo, usageLogRepo, usageBillingRepo, userRepo,
		userSubRepo, userGroupRateRepo, cache, cfg, schedulerSnapshot, concurrencyService,
		billingService, rateLimitService, billingCacheService, httpUpstream, deferredService,
		openAITokenProvider, grokTokenProvider, resolver, channelService, balanceNotifyService,
		settingService, userPlatformQuotaRepo)
	s.SetCodexTicketHistory(history)
	s.StartOpenAICodexTicketHarvester()
	return s
}
