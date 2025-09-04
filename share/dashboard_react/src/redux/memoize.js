import { createSelector } from "@reduxjs/toolkit";

export const selectRefreshInterval = state => state.cluster.refreshInterval;
export const selectClusterData = state => state.cluster.clusterData;
export const selectMonitor = state => state.globalClusters.monitor;
export const selectIsLogged = state => state.auth.isLogged;
export const selectUser = state => state.auth.user;
export const selectIsDesktop = state => state.common.isDesktop;
export const selectIsMobile = state => state.common.isMobile;

export const selectMeetUIState = createSelector(
  [
    (state) => state?.meet?.meetInfo,
    (state) => state?.meet?.unreadMessagesByChannel,
    (state) => state?.meet?.meetError,
  ],
  (meetInfo, unreadMessagesByChannel, meetError) => ({
    meetInfo,
    unreadMessagesByChannel,
    meetError,
  })
);
