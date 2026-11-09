import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/outbound-shipping-api",
    },
    {
      type: "category",
      label: "Packages",
      link: {
        type: "doc",
        id: "api-reference/rest/packages",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/stage-package",
          label: "Stage a manifested package (create a staging)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/load-package",
          label: "Load a package onto its load (create a loading)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/unload-package",
          label: "Unload a package back to staging (create an unloading)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Loads",
      link: {
        type: "doc",
        id: "api-reference/rest/loads",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/close-load",
          label: "Close a loading load (create a closure)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/reopen-load",
          label: "Reopen a closed load (create a reopening)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-load",
          label: "Cancel a planned load (create a cancellation)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-loads",
          label: "List outbound loads (keyset paginated)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-load",
          label: "Read one outbound load",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-load-packages",
          label: "List the packages of one load (keyset paginated)",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Bills of lading",
      link: {
        type: "doc",
        id: "api-reference/rest/bills-of-lading",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/void-bill-of-lading",
          label: "Void an issued bill of lading (create a void)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-bill-of-lading",
          label: "Read one bill of lading",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Sweeps",
      link: {
        type: "doc",
        id: "api-reference/rest/sweeps",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/run-missed-load-sweep",
          label: "Sweep for packages that missed their load (create a sweep)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Reports",
      link: {
        type: "doc",
        id: "api-reference/rest/reports",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-load-adherence-report",
          label: "Per-cutoff load adherence report",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-liveness",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-readiness",
          label: "Readiness probe",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
