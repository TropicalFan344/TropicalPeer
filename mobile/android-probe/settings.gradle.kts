// A bare Android app for checking the Go engine on a real phone (step 3 of
// docs/plan-android.md). The real app's UI comes later, from the Svelte
// frontend in a Capacitor shell.
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}
rootProject.name = "TropicalPeerProbe"
include(":app")
