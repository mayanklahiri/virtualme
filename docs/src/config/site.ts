const committedAnalyticsMeasurementId = "";
const environmentAnalyticsMeasurementId =
  (process.env.PUBLIC_GA_MEASUREMENT_ID ?? "").trim();

export const site = Object.freeze({
  productionUrl: "https://mayanklahiri.github.io/virtualme/",
  analyticsMeasurementId:
    environmentAnalyticsMeasurementId || committedAnalyticsMeasurementId,
});

if (site.analyticsMeasurementId && !/^G-[A-Z0-9]+$/.test(site.analyticsMeasurementId)) {
  throw new Error("PUBLIC_GA_MEASUREMENT_ID must match ^G-[A-Z0-9]+$");
}
