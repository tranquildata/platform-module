/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

#include <stdio.h>
#include <stdlib.h>
#include <dirent.h>
#include <string.h>
#include <sys/stat.h>
#include "output.h"

static const char INPUT_PATH[] = "/moduleio/input";
static const char OUTPUT_DIR[] = "/moduleio/output";

int produce_output(const char* output_name, const char* input_name, const char* output_content) {
  size_t pathLen = 100 + sizeof(OUTPUT_DIR);
  char outputPath[pathLen];
  snprintf(outputPath, pathLen, "%s/%s/%s", OUTPUT_DIR, input_name, output_name);
  FILE *outfile = fopen(outputPath, "w");
  if (outfile == NULL) {
    return -20;
  }
  int retval = 0;
  if (fputs(output_content, outfile) == EOF) {
    retval = -21;
  }
  fclose(outfile);
  return retval;
}

int create_output_dir(char dirName[]) {
    /*
    int rv = mkdir(OUTPUT_DIR, 0777);
    if (rv != 0) {
        printf("output dir 1 error %d\n", rv);
        strerror(rv);
        return -10;
    }
    */
    size_t pathLen = sizeof(OUTPUT_DIR) + 1 + strlen(dirName) + 1; //output directory prefix + '/' + directory name + '\0'
    char* path = malloc(pathLen);
    snprintf(path, pathLen, "%s/%s", OUTPUT_DIR, dirName);
    int rv = mkdir(path, 0777);
    if (rv != 0) {
        printf("output dir 2 error %d for %s\n", rv, path);
        strerror(rv);
        return -11;
    }
    free(path);
    return 0;
}

int main() {
  DIR *indir = opendir(INPUT_PATH);
  if (indir == NULL) {
    return -2;
  }

  struct dirent *entry;

  while ((entry = readdir(indir)) != NULL) {
    int pathLen = sizeof(INPUT_PATH) + strlen(entry->d_name) + 2;
    char* inFilename = malloc(pathLen);
    printf("entry: %s\n", entry->d_name);
    if (strcmp(entry->d_name, ".") == 0 || strcmp(entry->d_name, "..") == 0) {
        printf("skipping\n");
        continue;
    }

    strcpy(inFilename, INPUT_PATH);
    inFilename[sizeof(INPUT_PATH)] = '/';
    strcpy(inFilename + (sizeof(INPUT_PATH) + 1), entry->d_name);

    FILE *infile = fopen(inFilename, "r");
    if (infile == NULL) {
      printf("infile error\n");
      return -1;
    }
    int ch = fgetc(infile);
    while (ch != EOF) {
      // do something in a real system 
      ch = fgetc(infile);
    }
    fclose(infile);
    free(inFilename);
    // produce output
    int o = create_output_dir(entry->d_name);
    if (o != 0) {
        printf("create output error\n");
        return o;
    }
    o = produce_output("command.csv", entry->d_name, SAMPLE_COMMAND);
    if (o != 0) {
      printf("produce output 1 error\n");
      return o;
    }
    o = produce_output("housekeeping.csv", entry->d_name, SAMPLE_HOUSEKEEPING);
    if (o != 0) {
      printf("produce output 2 error\n");
      return o;
    }
    o = produce_output("output.csv", entry->d_name, SAMPLE_OUTPUT);
    if (o != 0) {
      printf("produce output 3 error\n");
      return o;
    }
  }
  closedir(indir);
  return 0;
}
