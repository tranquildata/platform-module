/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

#include <stdio.h>

int main() {
    char name[100];
    scanf("%100s", name); /* do a bounds checked scan */
    printf("hello %s\n", name);
    return 0;
}