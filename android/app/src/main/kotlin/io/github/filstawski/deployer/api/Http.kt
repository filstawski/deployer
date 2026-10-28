package io.github.filstawski.deployer.api

import java.util.concurrent.TimeUnit
import okhttp3.OkHttpClient

/**
 * The HTTP client for every server. Like the CLI's, it never follows
 * redirects, so the token can't be forwarded to another host.
 */
fun deployerHttpClient(): OkHttpClient = OkHttpClient.Builder()
    .followRedirects(false)
    .followSslRedirects(false)
    .connectTimeout(10, TimeUnit.SECONDS)
    .callTimeout(30, TimeUnit.SECONDS)
    .build()
