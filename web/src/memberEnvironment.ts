export const memberPortal: boolean = false;
export const memberLogin = "/";
export const memberAPI = "/sandbox/workspace";

export type WorkspaceCapabilities = {
  createSites: boolean;
  teamManagement: boolean;
  mfaEnrollment: boolean;
  siteRouting: boolean;
  routeAccessHistory: boolean;
  payoutWorkflow: boolean;
  statementReconciliation: boolean;
  settlementConfirmations: boolean;
  chargebacks: boolean;
  paymentDemos: boolean;
};

export const sandboxCapabilities: WorkspaceCapabilities = {
  createSites: true,
  teamManagement: true,
  mfaEnrollment: true,
  siteRouting: true,
  routeAccessHistory: false,
  payoutWorkflow: true,
  statementReconciliation: true,
  settlementConfirmations: false,
  chargebacks: true,
  paymentDemos: true,
};
